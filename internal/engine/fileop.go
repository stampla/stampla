package engine

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// The scratch name a copy lands under before it is finalized.
//
// It begins with a dot so the scanner skips it: a half-written file that
// a later run classified as media would be given an identity derived
// from incomplete bytes. It embeds the target's own name so a run can
// recognize exactly the leftovers belonging to the target it is about to
// write, and the writing process's id so two runs never pick the same
// scratch file.
const (
	scratchPrefix = "."
	scratchMiddle = ".stampla-"
	scratchSuffix = ".part"
)

func scratchBase(target string) string {
	return scratchPrefix + target + scratchMiddle + strconv.Itoa(os.Getpid()) + scratchSuffix
}

// isScratchFor reports whether a directory entry is a scratch file for
// this target, from this run or an interrupted one.
func isScratchFor(name, target string) bool {
	return strings.HasPrefix(name, scratchPrefix+target+scratchMiddle) &&
		strings.HasSuffix(name, scratchSuffix)
}

// dropStaleScratch removes the leftovers an interrupted run left for
// this target.
//
// A scratch file is only ever the incomplete left half of a copy: the
// finalize is a rename, so bytes that reached the target name were
// verified first and a scratch file has by definition not reached it.
// Deleting one therefore destroys no information. Without an archive
// lock this can also delete the scratch of a second stampla writing the
// same target at the same time; that run's copy then fails loudly at its
// own finalize rather than producing anything wrong, which is the trade
// the no-lock design makes.
func dropStaleScratch(dir, target string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && isScratchFor(entry.Name(), target) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// copyInto copies src to target without ever overwriting anything, and
// proves the copy before it counts.
//
// The bytes land under a scratch name in the target's own directory —
// the same directory so that the finalize is a rename within one
// filesystem, which is the only kind that is atomic — while a whole-file
// digest is taken of what streamed past. The scratch file is fsynced,
// then read back and digested again: a transfer that lost, truncated or
// reordered a byte is caught here, before anything wears an identity
// name. Only then is the scratch claimed onto the target, and the
// directory fsynced so the claim survives a power cut.
//
// The read-back is of what the filesystem hands back after a sync, which
// catches the errors a copy actually makes — a short write, a bad cable,
// a full disk reported late — rather than proving anything about the
// platter. Proving the platter is not something a userspace program can
// do, and claiming to would be worse than not trying.
//
// The digest is deliberately over the whole file rather than over the
// image payload the identity is cut from: a copy must reproduce its
// source exactly, metadata included, and a payload-only digest would not
// notice a mangled header.
func copyInto(src, target string) error {
	dir, base := filepath.Split(target)
	dir = filepath.Clean(dir)
	dropStaleScratch(dir, base)
	scratch := filepath.Join(dir, scratchBase(base))

	// Created here rather than inside the copy so that the cleanup below
	// can only ever remove a file this call made.
	out, err := os.OpenFile(scratch, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	written, digest, err := streamCopy(src, out)
	if err == nil {
		err = verifyLanded(scratch, src, written, digest)
	}
	if err == nil {
		err = claimRename(scratch, target)
	}
	if err != nil {
		_ = os.Remove(scratch)
		return err
	}
	syncDir(dir)
	return nil
}

// streamCopy writes src into an already created scratch file, digesting
// what it writes, and returns the byte count and the digest. It closes
// the file whatever happens.
func streamCopy(src string, out *os.File) (written int64, digest string, err error) {
	in, err := os.Open(src)
	if err != nil {
		_ = out.Close()
		return 0, "", err
	}
	defer func() { _ = in.Close() }()

	sum := md5.New()
	buf := make([]byte, hashChunk)
	written, err = io.CopyBuffer(io.MultiWriter(out, sum), in, buf)
	if err == nil {
		// Data before metadata: the bytes must be on the platter before
		// the name that promises them is.
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", fmt.Errorf("copying %s: %w", src, err)
	}
	return written, hex.EncodeToString(sum.Sum(nil)), nil
}

// verifyLanded re-reads a scratch file and refuses it unless it is
// byte-for-byte what was streamed into it.
func verifyLanded(scratch, src string, written int64, digest string) error {
	info, err := os.Stat(scratch)
	if err != nil {
		return fmt.Errorf("verifying the copy of %s: %w", src, err)
	}
	if info.Size() != written {
		return fmt.Errorf("verifying the copy of %s: %d bytes landed, %d were written",
			src, info.Size(), written)
	}
	landed, err := md5Of(scratch)
	if err != nil {
		return fmt.Errorf("verifying the copy of %s: %w", src, err)
	}
	if landed != digest {
		return fmt.Errorf("the copy of %s does not match what was read from it"+
			" (%s on disk, %s in transit) — transfer error; the source is untouched",
			src, landed, digest)
	}
	return nil
}

// claimRename renames old to new without ever overwriting new.
//
// POSIX rename silently replaces its target, which is the one thing this
// tool may never do, so the target is claimed with a hard link first:
// link fails atomically when the name is taken, and the source is only
// unlinked once the claim is proven to be the same file. Windows rename
// refuses an existing target natively and is used as it is. A filesystem
// with no hard links falls back to checking and then renaming, which is
// racy against another writer and is documented as the weaker guarantee
// it is.
//
// The window between the link and the unlink is a real crash window: it
// leaves one file under two names. It is recognizable — the two names
// are the same inode — and completing it is what the next run does.
func claimRename(old, target string) error {
	if isCaseOnlyRename(old, target) {
		return caseOnlyRename(old, target)
	}
	if runtime.GOOS == "windows" {
		if err := os.Rename(old, target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return &ExistsError{Path: target}
			}
			return err
		}
		return nil
	}

	err := os.Link(old, target)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrExist):
		return &ExistsError{Path: target}
	case crossDevice(err):
		return err
	default:
		// No hard links here (FAT, some network mounts). Nothing atomic
		// is available, so look before leaping and accept the race.
		if _, statErr := os.Lstat(target); statErr == nil {
			return &ExistsError{Path: target}
		}
		return os.Rename(old, target)
	}

	if err := sameFile(old, target); err != nil {
		// The claim is not our file. Leave both names alone: removing
		// either could be the removal of somebody else's data.
		return err
	}
	return os.Remove(old)
}

// sameFile reports an error unless the two paths name one file.
func sameFile(a, b string) error {
	infoA, err := os.Stat(a)
	if err != nil {
		return err
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return err
	}
	if !os.SameFile(infoA, infoB) {
		return fmt.Errorf("%s and %s are not the same file after the link claim", a, b)
	}
	return nil
}

// isCaseOnlyRename reports whether old and new are one directory entry
// under two spellings — the .FP2 to .fp2 rename, which on APFS, NTFS and
// exFAT is a change to an entry that already "exists" as itself.
//
// The three conditions together can only hold on a case-insensitive
// filesystem: on a case-sensitive one the two names are two files (or
// one and a hole), and the ordinary claim path applies.
func isCaseOnlyRename(old, target string) bool {
	if filepath.Dir(old) != filepath.Dir(target) {
		return false
	}
	oldBase, newBase := filepath.Base(old), filepath.Base(target)
	if oldBase == newBase || !strings.EqualFold(oldBase, newBase) {
		return false
	}
	return sameFile(old, target) == nil
}

// caseOnlyRename changes the case of a directory entry.
//
// A direct rename is tried first: where it works it is one atomic
// operation on one entry, with no window in which the file is missing.
// Filesystems that refuse it — some SMB and exFAT drivers reject a
// rename whose target compares equal to its source — get the two-step
// through a temporary name, which is reverted if the second half fails.
// The temporary keeps the file's extension so that a crash between the
// two halves leaves a file the next scan still sees and converges.
func caseOnlyRename(old, target string) error {
	if err := os.Rename(old, target); err == nil {
		return nil
	}
	return caseSwapViaTemp(old, target)
}

// caseSwapViaTemp renames through a temporary name in the same
// directory, putting the file back if the second half fails.
//
// The temporary keeps the file's extension deliberately: a crash between
// the two renames leaves a file the next scan still recognizes as media
// and still converges, rather than one that has quietly stopped being
// anything the tool owns. It is not a scratch name either, so the
// stale-scratch cleanup — which deletes what it finds — will never touch
// it.
func caseSwapViaTemp(old, target string) error {
	base := filepath.Base(target)
	ext := filepath.Ext(base)
	tmp := filepath.Join(filepath.Dir(target),
		strings.TrimSuffix(base, ext)+"-case"+strconv.Itoa(os.Getpid())+ext)
	if err := os.Rename(old, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		if back := os.Rename(tmp, old); back != nil {
			return fmt.Errorf("%w; the file is left at %s", err, tmp)
		}
		return err
	}
	return nil
}

// crossDevice reports whether an error says the two paths live on
// different filesystems, which is the signal to copy and verify instead
// of renaming.
func crossDevice(err error) bool {
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	// Windows reports ERROR_NOT_SAME_DEVICE, which Go surfaces as a raw
	// Errno rather than as EXDEV.
	var errno syscall.Errno
	return errors.As(err, &errno) && uintptr(errno) == 0x11
}

// syncDir flushes a directory's own entries, so a rename into it
// survives a power cut. Not every filesystem can, and failing to costs
// durability rather than correctness.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// resolveDeepest resolves the symlinks of a path that may not exist yet,
// by resolving its deepest existing ancestor and re-appending the rest.
// A target directory that has not been created is judged by the
// directory it would be created in, which is the one that could be a
// link out of the archive.
func resolveDeepest(path string) (string, error) {
	current := filepath.Clean(path)
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if rest == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolving %s: no existing ancestor", path)
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

// contained refuses a path that does not resolve to a location under the
// root.
//
// The check exists because a directory inside an archive can be a
// symlink pointing anywhere, and a rename or a copy following it would
// write outside the tree the user named — silently, and past every
// no-clobber guarantee, which only ever covered the target name. It is
// run again immediately before each mutation and not only at plan time:
// a directory swapped for a link in between must not let the write
// through.
func contained(root, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving the destination root %s: %w", root, err)
	}
	resolved, err := resolveDeepest(path)
	if err != nil {
		return err
	}
	if !under(resolvedRoot, resolved) {
		return &EscapeError{Path: path, Resolved: resolved, Root: resolvedRoot}
	}
	return nil
}

// under reports whether path is the root or sits beneath it.
//
// Comparison folds case where the platform's filesystems do, because
// there two spellings of one directory are one directory and a
// case-sensitive prefix test would call a file inside the root an escape
// from it.
func under(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if caseInsensitiveFS {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// caseInsensitiveFS is whether this platform's usual filesystems fold
// case in path lookups. It is a property of the platform rather than of
// a mounted volume, which is the coarse answer path comparison needs:
// treating a case-sensitive volume as insensitive can only merge two
// spellings that the volume itself would keep apart, and both of them
// are still inside the root.
var caseInsensitiveFS = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// absPath is the absolute form of a path, for comparing and recording.
// A path that cannot be made absolute is cleaned and returned, because a
// comparison that is merely approximate beats one that panics.
func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
