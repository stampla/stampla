package cli

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/stampla/stampla/internal/scanner"
)

// removablePrefix is the directory this platform mounts removable media
// under, when file is somewhere beneath one.
//
// This is the pure half of the heuristic: a path question with a fixed
// answer per platform, which is what makes the whole predicate testable
// for every platform from any of them. The impure half — whether the
// thing mounted there is really a volume of its own — is mountPointOf.
//
// Windows is deliberately not detected in v0.1: a card reader is given
// an ordinary drive letter, indistinguishable from an internal disk
// without asking the volume manager, and a confirmation that fired on
// every second disk would be one users learn to answer without reading.
func removablePrefix(goos, file string) (string, bool) {
	var prefixes []string
	switch goos {
	case "darwin":
		prefixes = []string{"/Volumes"}
	case "linux":
		// /run/media first: it is not under /media, but naming it first
		// keeps the list readable as "the newer convention, then the
		// older one".
		prefixes = []string{"/run/media", "/media"}
	default:
		return "", false
	}
	clean := path.Clean(file)
	for _, prefix := range prefixes {
		if strings.HasPrefix(clean, prefix+"/") && len(clean) > len(prefix)+1 {
			return prefix, true
		}
	}
	return "", false
}

// removableSource is the volume root of the first source that sits on
// removable media, and empty when none does.
//
// Sources already under the destination are not asked about: moving a
// file within the archive it is already in takes no copy away from
// anywhere. Which matters, because an archive can perfectly well live on
// an external disk mounted under /Volumes, and being asked to confirm
// every rename in it would teach a user to answer without reading.
func removableSource(scan *scanner.Scan, dest string) string {
	if scan == nil {
		return ""
	}
	asked := make(map[string]string)
	for _, group := range scan.Groups {
		for _, member := range group.Members {
			abs, err := filepath.Abs(member.Path)
			if err != nil || under(dest, abs) {
				continue
			}
			prefix, ok := removablePrefix(runtime.GOOS, filepath.ToSlash(abs))
			if !ok {
				continue
			}
			dir := filepath.Dir(abs)
			root, seen := asked[dir]
			if !seen {
				root = removableRoot(prefix, dir)
				asked[dir] = root
			}
			if root != "" {
				return root
			}
		}
	}
	return ""
}

// removableRoot is the volume dir sits on, when that volume is really
// mounted under prefix rather than being part of the boot volume.
//
// macOS mounts the boot volume under /Volumes too, so the mount point is
// what tells a memory card from the disk the system is running on; Linux
// puts a user directory between /media and the volume, so the mount
// point is also what says where the volume actually starts.
func removableRoot(prefix, dir string) string {
	mount := mountPointOf(dir)
	slashed := path.Clean(filepath.ToSlash(mount))
	if !strings.HasPrefix(slashed, prefix+"/") {
		// The mount point is at or above the prefix, which means nothing
		// is mounted there: the path is an ordinary directory of the boot
		// volume that happens to live under /Volumes or /media.
		return ""
	}
	return mount
}

// mountPointOf is the deepest directory at or above dir whose
// filesystem differs from its parent's: the volume dir sits on.
//
// A directory that cannot be compared with its parent is treated as part
// of the same filesystem, so the walk keeps climbing and the answer ends
// up outside the prefix. Not knowing produces no confirmation rather
// than a confirmation about a volume nobody can name.
func mountPointOf(dir string) string {
	for {
		parent := filepath.Dir(dir)
		if parent == dir || !sameDevice(dir, parent) {
			return dir
		}
		dir = parent
	}
}

// under reports whether path is root itself or somewhere beneath it.
func under(root, file string) bool {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
