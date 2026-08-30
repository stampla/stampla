package scanner

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stampla/stampla/internal/identity"
)

// expand turns the selected files into groups. Selection is literal;
// groups are not: a group converges atomically, so every member on disk
// comes along, and the labeled-derivative merge decides which keys are
// one group in the first place.
func (c *collector) expand() {
	selected := slices.SortedFunc(maps.Values(c.items), func(a, b *Item) int {
		return strings.Compare(a.Path, b.Path)
	})

	candidates := make(map[string][]string)
	found := make(map[string]bool, len(selected))
	add := func(path string) {
		key := absKey(path)
		if found[key] {
			return
		}
		found[key] = true
		group := identity.GroupKey(path)
		candidates[group] = append(candidates[group], path)
	}
	// The selected files go in first so that their spelling — the one
	// the input used — is the one reported.
	for _, item := range selected {
		add(item.Path)
	}
	for _, item := range selected {
		for _, dir := range c.neighborhood(homeDir(item.Path)) {
			for _, entry := range c.entries(dir) {
				if entry.IsDir() || hidden(entry.Name()) {
					continue
				}
				if path := filepath.Join(dir, entry.Name()); owned(path) {
					add(path)
				}
			}
		}
	}

	merged := labeledMerge(candidates)
	groups := make(map[string][]string, len(candidates))
	for _, key := range slices.Sorted(maps.Keys(candidates)) {
		target := merged[key]
		groups[target] = append(groups[target], candidates[key]...)
	}

	for _, key := range slices.Sorted(maps.Keys(groups)) {
		paths := groups[key]
		// The neighborhood holds every group in the directories it
		// touched; only the ones the input actually selected are this
		// run's work.
		if !slices.ContainsFunc(paths, func(path string) bool {
			_, selected := c.items[absKey(path)]
			return selected
		}) {
			continue
		}
		if members := c.members(paths); len(members) > 0 {
			c.scan.Groups = append(c.scan.Groups, Group{Key: key, Members: members})
		}
	}
}

// neighborhood is where a group's members can be: its home directory and
// that directory's immediate subdirectories, which is where a vendor
// sidecar directory sits. Under a mutation verb a subdirectory that is
// another archive is left alone — stopping at nested roots would mean
// little if group expansion reached into them anyway.
func (c *collector) neighborhood(home string) []string {
	dirs := []string{home}
	for _, entry := range c.entries(home) {
		if !entry.IsDir() || hidden(entry.Name()) {
			continue
		}
		dir := filepath.Join(home, entry.Name())
		if c.opts.StopAtRoots {
			if declares, unknown := c.marker(dir); declares || unknown {
				continue
			}
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// members builds one group's items, ordered master first. A member that
// vanished between the listing and here is a finding: the group is the
// unit of work, and a hole in one must not pass unmentioned.
func (c *collector) members(paths []string) []Item {
	members := make([]Item, 0, len(paths))
	for _, path := range slices.Sorted(slices.Values(paths)) {
		if item, selected := c.items[absKey(path)]; selected {
			members = append(members, *item)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			c.unscannable(path, err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		members = append(members, Item{
			Path:    path,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Implied: true,
		})
	}
	slices.SortFunc(members, func(a, b Item) int {
		if d := memberRank(a.Path) - memberRank(b.Path); d != 0 {
			return d
		}
		return strings.Compare(a.Path, b.Path)
	})
	return members
}

// The member order within a group: the camera-native master carries the
// identity everything else in the group inherits, so it leads; a file
// that could master a group but was written by an editor comes next;
// sidecars and labeled derivatives follow.
const (
	rankMaster = iota
	rankLooseMaster
	rankDerived
)

func memberRank(path string) int {
	base := filepath.Base(path)
	if id, ok := identity.ParseName(base); ok {
		switch {
		case !id.IsMaster():
			return rankDerived
		case identity.CameraNative(id.Ext):
			return rankMaster
		default:
			return rankLooseMaster
		}
	}
	// Not a canonical name: judge by the name's own shape, which is what
	// a not-yet-named file has.
	switch {
	case identity.IsSidecar(path) || !identity.IsMedia(base):
		return rankDerived
	case identity.CameraNative(filepath.Ext(base)):
		return rankMaster
	default:
		return rankLooseMaster
	}
}

// labeledMerge maps every group key to the key its group converges
// under. A group whose base extends another's with a "-" or "_" label is
// that group's derivative — but only when the shorter base owns a
// camera-native master and the labeled one does not. A labeled group
// with its own master is a separate photo, not an edit (IMG_01.NEF
// beside IMG.NEF), and where several shorter bases qualify the longest
// wins. Only groups in one directory are compared; a key that carries no
// directory is a not-yet-named file beside the working directory, and
// compares there.
//
// The rule needs no exception for canonically named groups: every prefix
// is the same width, so no prefix can extend another, and a labeled
// derivative of a named master already shares its prefix.
func labeledMerge(candidates map[string][]string) map[string]string {
	native := make(map[string]bool, len(candidates))
	for key, paths := range candidates {
		native[key] = slices.ContainsFunc(paths, func(path string) bool {
			return identity.CameraNative(filepath.Ext(path))
		})
	}

	merged := make(map[string]string, len(candidates))
	for key := range candidates {
		merged[key] = key
		if native[key] {
			continue
		}
		dir, base := filepath.Dir(key), filepath.Base(key)
		parent := ""
		for other := range candidates {
			candidate := filepath.Base(other)
			if !native[other] || filepath.Dir(other) != dir {
				continue
			}
			if len(candidate) >= len(base) || !strings.HasPrefix(base, candidate) {
				continue
			}
			if label := base[len(candidate)]; label != '-' && label != '_' {
				continue
			}
			if len(candidate) > len(parent) {
				parent = candidate
			}
		}
		if parent != "" {
			merged[key] = filepath.Join(dir, parent)
		}
	}
	return merged
}

// homeProbe prefixes a name so that it cannot be canonical, which makes
// identity.GroupKey answer in the not-yet-named regime — the one that
// resolves a vendor sidecar subdirectory back to the master's directory.
// Asking identity where a file's group lives beats repeating its
// sidecar-directory table here.
const homeProbe = "x"

// homeDir is the directory a file's group lives in: its own, or the
// master's when the file sits in a vendor sidecar subdirectory.
func homeDir(path string) string {
	dir, base := filepath.Split(path)
	return filepath.Dir(identity.GroupKey(filepath.Join(dir, homeProbe+base)))
}
