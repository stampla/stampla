package scanner

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/identity"
	"github.com/stampla/stampla/internal/layout"
)

// Item is one file the run will operate on.
type Item struct {
	// Path is the file as the scan encountered it: the path the input
	// named, or one built from the directory it named. Reports quote it
	// back, so it is never rewritten into some other spelling.
	Path string
	// Size is the file's size in bytes, as the walk saw it.
	Size int64
	// ModTime is the file's modification time, as the walk saw it.
	// Neither field is evidence about identity — only the file's content
	// and metadata are — but a run needs both to move bytes and to show
	// progress.
	ModTime time.Time
	// Implied is true for a member the scan pulled in because its group
	// was selected rather than because the input named it. Group
	// atomicity beats literal selection; this says which files that
	// brought along.
	Implied bool
}

// Kind is the media a group converges as.
//
// A group never spans photo and video. Same-base stills are one group —
// a RAW and its JPEG twin share a capture and converge together — but a
// photo and a video that share a base name (IMG_1234.HEIC beside
// IMG_1234.MOV, the shape every Live Photo has) are two identities. One
// name can assert only one of them, and a still renamed onto a clip's
// prefix would claim a capture it never had; worse, as a group member
// rather than a master it would never be content-checked again, so the
// false claim would survive every later verify.
type Kind string

const (
	// KindPhoto is a still group: RAW, JPEG, HEIC, TIFF, DNG.
	KindPhoto Kind = "photo"
	// KindVideo is a moving-image group.
	KindVideo Kind = "video"
)

// Group is a master and everything that renames with it.
type Group struct {
	// Key is the identity.GroupKey the members share, after the
	// labeled-derivative merge. It names the group's base, which is what
	// a member's name is stemmed against.
	Key string
	// Kind is the media the group converges as. The key alone does not
	// identify a group — a photo and a video sharing a base name are two
	// groups with one key — so anything indexing groups keys on ID.
	Kind Kind
	// Members are the group's files: the master first when the group has
	// one, then sidecars and derivatives by path. A group either fully
	// converges or is reported, so the order is the order to act in.
	Members []Item
}

// ID identifies one group among a scan's groups: the key names the base
// its members share, and the kind tells the still apart from the clip
// that shares that base.
func (g Group) ID() string { return g.Key + "#" + string(g.Kind) }

// Options configures a collection.
type Options struct {
	// Stdin, when non-nil, replaces the inputs with a list read from it.
	Stdin io.Reader
	// NulSep reads that list NUL-delimited (-z, for find -print0)
	// instead of newline-delimited.
	NulSep bool
	// StopAtRoots stops recursion at a nested archive root, for the
	// mutation verbs. The verify verb leaves it false and descends.
	StopAtRoots bool
	// KeepUnowned collects the files recursion would otherwise filter
	// away for being in a format stampla does not own, so the caller can
	// account for them rather than count them.
	//
	// It exists for the membership check, whose exit code says whether a
	// memory card may be formatted: a file the report never mentioned is
	// a file the answer did not cover, and "not mentioned" and "not
	// there" must never look alike to somebody about to erase the
	// original. Dot-files are still skipped — a hidden file is not one a
	// card holds a photograph in — and every mutation verb leaves this
	// false, because a format stampla owns no identity for is not a
	// format it renames.
	KeepUnowned bool
}

// Skipped counts what recursion passed over. None of it is a finding —
// these are files the tool does not own — but a run reports the counts,
// because an archive that appears to hold nothing and one whose files
// were all filtered away must not read alike.
type Skipped struct {
	// Hidden counts dotfiles and dot-directories, .stampla markers among
	// them. A skipped directory counts once; its contents are never
	// visited and never counted.
	Hidden int
	// Other counts entries in formats the scan does not own: neither
	// media nor sidecar, or not a regular file at all.
	Other int
}

// Scan is what one collection found.
type Scan struct {
	// Groups are the convergence groups, ordered by Key.
	Groups []Group
	// NestedRoots are the directories whose marker declared a layout, in
	// the order recursion met them. The verify verb re-runs each under
	// its own declaration.
	NestedRoots []string
	// Errors are the paths the scan could not account for. They are
	// findings, never silences.
	Errors []finding.Finding
	// Skipped counts what recursion filtered away.
	Skipped Skipped
}

// Collect gathers the inputs and expands them into groups. It returns an
// error only when the input list itself cannot be read; every per-path
// trouble is a finding in the returned Scan, so one bad path never
// aborts a run that could still report the rest.
func Collect(inputs []string, opts Options) (*Scan, error) {
	if opts.Stdin != nil {
		list, err := readList(opts.Stdin, opts.NulSep)
		if err != nil {
			return nil, err
		}
		inputs = list
	}
	c := newCollector(opts)
	for _, input := range inputs {
		c.add(input)
	}
	c.expand()
	return c.scan, nil
}

// readList splits a --stdin file list. A trailing terminator is
// tolerated, since every producer writes one, and in newline mode so is
// a CRLF line ending: a list produced on Windows must name the same
// files. NUL mode strips nothing — that is what -print0 is for. Empty
// entries are dropped; a blank line is not a path.
func readList(r io.Reader, nulSep bool) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the input list: %w", err)
	}
	separator := "\n"
	if nulSep {
		separator = "\x00"
	}
	var list []string
	for _, entry := range strings.Split(string(data), separator) {
		if !nulSep {
			entry = strings.TrimSuffix(entry, "\r")
		}
		if entry != "" {
			list = append(list, entry)
		}
	}
	return list, nil
}

// markerState is what one directory's marker said, cached because both
// recursion and the group neighborhood ask.
type markerState struct {
	declares bool // the marker declares a layout: another archive
	unknown  bool // the marker could not be read, so neither answer holds
}

type collector struct {
	opts Options
	scan *Scan

	// items are the selected files, keyed by absolute path so that two
	// spellings of one file select it once.
	items map[string]*Item
	// walked marks directories recursion has descended into, so
	// overlapping inputs do not walk the same tree twice.
	walked map[string]bool
	// dirs caches directory listings; a failed listing caches as nil, to
	// report the trouble once.
	dirs map[string][]fs.DirEntry
	// markers caches the marker answer per directory.
	markers map[string]markerState
	// recorded deduplicates findings: the same path reached twice is one
	// piece of news.
	recorded map[string]bool
}

func newCollector(opts Options) *collector {
	return &collector{
		opts:     opts,
		scan:     &Scan{},
		items:    make(map[string]*Item),
		walked:   make(map[string]bool),
		dirs:     make(map[string][]fs.DirEntry),
		markers:  make(map[string]markerState),
		recorded: make(map[string]bool),
	}
}

// add takes one input: a directory recurses, anything else is literal.
func (c *collector) add(input string) {
	if input == "" {
		c.record(finding.Missing, input, "empty path in the input list")
		return
	}
	path := filepath.Clean(input)
	// Explicit inputs are stat'd, not lstat'd: a symlink the user named
	// is a way of naming the file it points at.
	info, err := os.Stat(path)
	if err != nil {
		c.unscannable(path, err)
		return
	}
	if info.IsDir() {
		c.walk(path)
		return
	}
	switch {
	case !info.Mode().IsRegular():
		c.record(finding.Unresolvable, path, "not a regular file")
	case !owned(path):
		c.record(finding.Unresolvable, path,
			"neither a photo or video stampla names nor a sidecar")
	default:
		c.selectFile(path, info)
	}
}

// walk recurses a directory input. The error callback is wired to
// findings and never suppressed: a directory that will not list is
// exactly the silence this tool exists to prevent.
func (c *collector) walk(root string) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// WalkDir reports a directory it could not list here, having
			// already reported the directory itself. Recording and
			// carrying on keeps the rest of the tree scannable while the
			// gap stays visible.
			c.unscannable(path, err)
			return nil
		}
		if d.IsDir() {
			return c.enterDir(root, path)
		}
		c.visitFile(path, d)
		return nil
	})
	if err != nil {
		c.unscannable(root, err)
	}
}

// enterDir decides whether recursion descends into a directory. The
// input root itself is always entered: the user named it.
func (c *collector) enterDir(root, path string) error {
	if path != root {
		if hidden(filepath.Base(path)) {
			c.scan.Skipped.Hidden++
			return fs.SkipDir
		}
		declares, unknown := c.marker(path)
		if declares {
			c.addNestedRoot(path)
		}
		// Another archive inside this one is not a mutation's business.
		// An unreadable marker stops a mutation too: not knowing is not
		// the same as knowing there is nothing there.
		if (declares || unknown) && c.opts.StopAtRoots {
			return fs.SkipDir
		}
	}
	if c.walked[absKey(path)] {
		return fs.SkipDir // reached again through an overlapping input
	}
	c.walked[absKey(path)] = true
	return nil
}

// visitFile applies the recursion filter to one entry.
func (c *collector) visitFile(path string, d fs.DirEntry) {
	if hidden(d.Name()) {
		c.scan.Skipped.Hidden++
		return
	}
	if !owned(path) && !c.opts.KeepUnowned {
		c.scan.Skipped.Other++
		return
	}
	info, err := d.Info()
	if err != nil {
		c.unscannable(path, err)
		return
	}
	if !info.Mode().IsRegular() {
		// A link named like a photo is followed once, so that a
		// symlinked file is not lost; a link to a directory is not
		// descended — WalkDir does not follow links, and a loop would
		// never end. Name such a directory to scan it.
		target, err := os.Stat(path)
		if err != nil {
			c.unscannable(path, err)
			return
		}
		if !target.Mode().IsRegular() {
			c.scan.Skipped.Other++
			return
		}
		info = target
	}
	c.selectFile(path, info)
}

// selectFile records a file the input selected. The first spelling of a
// path wins, so an explicit argument keeps the name the user typed.
func (c *collector) selectFile(path string, info os.FileInfo) {
	key := absKey(path)
	if _, taken := c.items[key]; taken {
		return
	}
	c.items[key] = &Item{Path: path, Size: info.Size(), ModTime: info.ModTime()}
}

// marker reports whether a directory declares a layout of its own — a
// nested archive root — and whether the answer is unknown because the
// marker would not read. An unreadable marker is a finding either way.
func (c *collector) marker(dir string) (declares, unknown bool) {
	key := absKey(dir)
	if state, cached := c.markers[key]; cached {
		return state.declares, state.unknown
	}
	var state markerState
	m, err := layout.ReadMarker(dir)
	switch {
	case err != nil:
		// Not knowing whether this is another archive is itself a gap in
		// the scan, and the usual cause is the directory being
		// unreadable altogether.
		state.unknown = true
		c.unscannable(dir, err)
	case m != nil && m.HasLayout():
		state.declares = true
	}
	c.markers[key] = state
	return state.declares, state.unknown
}

// entries lists a directory once, for the group neighborhood. A listing
// that fails is a finding and reads as empty.
func (c *collector) entries(dir string) []fs.DirEntry {
	key := absKey(dir)
	if entries, cached := c.dirs[key]; cached {
		return entries
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.unscannable(dir, err)
		entries = nil
	}
	c.dirs[key] = entries
	return entries
}

func (c *collector) addNestedRoot(dir string) {
	key := "root\x00" + absKey(dir)
	if c.recorded[key] {
		return
	}
	c.recorded[key] = true
	c.scan.NestedRoots = append(c.scan.NestedRoots, dir)
}

// unscannable records a path the scan could not account for. Silence
// here is the failure mode the tool exists to prevent: files that exist
// but were never seen must not pass as files that were not there.
func (c *collector) unscannable(path string, err error) {
	c.record(finding.Missing, path, fmt.Sprintf("cannot scan: %v", err))
}

// record files a finding, once per path and class: a directory that
// refuses both its marker and its listing is one piece of news, and the
// first reason is the reason.
func (c *collector) record(class finding.Class, path, detail string) {
	key := string(class) + "\x00" + path
	if c.recorded[key] {
		return
	}
	c.recorded[key] = true
	c.scan.Errors = append(c.scan.Errors, finding.Finding{Class: class, Path: path, Detail: detail})
}

// owned reports whether the scan keeps a file: a photo or video stampla
// names, or a sidecar that renames with one. What counts as either is
// identity's business, not this package's.
func owned(path string) bool {
	return identity.IsMedia(path) || identity.IsSidecar(path)
}

// hidden reports whether a name is a dotfile or dot-directory. The
// .stampla marker is one: it is read as a declaration, never converged.
func hidden(name string) bool { return strings.HasPrefix(name, ".") }

// absKey identifies a file across spellings, so that an explicit
// argument and the same file reached by recursion are one file — and so
// that they land in one group. It is a key, never a path to report:
// filepath.Abs does not resolve links or fold case, so two genuinely
// different spellings of one file on a case-insensitive volume still
// count twice.
func absKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
