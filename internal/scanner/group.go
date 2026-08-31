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

	// One base name can name a still and a clip; they are two groups
	// before anything else is decided about either.
	buckets := make(map[bucket][]string, len(candidates))
	for key, paths := range candidates {
		for kind, members := range splitKinds(paths) {
			buckets[bucket{key: key, kind: kind}] = members
		}
	}

	merged := labeledMerge(buckets)
	groups := make(map[bucket][]string, len(buckets))
	for _, id := range sortedBuckets(buckets) {
		target := merged[id]
		groups[target] = append(groups[target], buckets[id]...)
	}

	for _, id := range sortedBuckets(groups) {
		paths := groups[id]
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
			c.scan.Groups = append(c.scan.Groups,
				Group{Key: id.key, Kind: id.kind, Members: members})
		}
	}
}

// bucket is a group while the scan is still building it: the key its
// members share and the media they converge as. Both halves are needed —
// the photo and the video that share a base name share a key.
type bucket struct {
	key  string
	kind Kind
}

// sortedBuckets orders groups for a deterministic scan: by key, then by
// kind, which puts the photo group of a contested base name before the
// video one.
func sortedBuckets[V any](groups map[bucket]V) []bucket {
	ids := slices.Collect(maps.Keys(groups))
	slices.SortFunc(ids, func(a, b bucket) int {
		if d := strings.Compare(a.key, b.key); d != 0 {
			return d
		}
		return strings.Compare(string(a.kind), string(b.kind))
	})
	return ids
}

// splitKinds divides one key's files into the groups they converge as. A
// group never spans photo and video, so a base name that names both is
// two groups here, each with its own master and its own identity.
//
// A file's kind is the kind of the master its name names: its own
// extension for a media file, and for a sidecar the master extension its
// name chain carries — DSC_1234.MP4.xmp names the clip,
// NKSC_PARAM/DSC_1234.NEF.nksc the still. A sidecar carrying no master
// extension (a bare DSC_1234.xmp) claims neither, and the longer claim
// wins: it joins the photo group wherever the base has one, since bare
// sidecars are what photo tools write, and the video group only when
// there is no photo group to join. A base name with nothing but bare
// sidecars converges as a photo group.
func splitKinds(paths []string) map[Kind][]string {
	kinds := make(map[Kind][]string, 2)
	var unclaimed []string
	for _, path := range paths {
		if kind, claimed := claim(path); claimed {
			kinds[kind] = append(kinds[kind], path)
		} else {
			unclaimed = append(unclaimed, path)
		}
	}
	if len(unclaimed) > 0 {
		home := KindPhoto
		if len(kinds[KindPhoto]) == 0 && len(kinds[KindVideo]) > 0 {
			home = KindVideo
		}
		kinds[home] = append(kinds[home], unclaimed...)
	}
	return kinds
}

// claim is the media kind a file's name names, and whether it names one
// at all. A media file claims its own kind; a sidecar claims the kind of
// the master extension appended in its name (DSC_1234.NEF.xmp, and the
// same shape under a vendor sidecar directory). Everything else claims
// nothing.
func claim(path string) (Kind, bool) {
	base := filepath.Base(path)
	if identity.IsMedia(base) {
		return kindOf(filepath.Ext(base)), true
	}
	if stem := strings.TrimSuffix(base, filepath.Ext(base)); identity.IsMedia(stem) {
		return kindOf(filepath.Ext(stem)), true
	}
	return KindPhoto, false
}

func kindOf(ext string) Kind {
	if identity.IsVideo(ext) {
		return KindVideo
	}
	return KindPhoto
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

// labeledMerge maps every group to the group it converges under. A group
// whose base extends another's with a "-" or "_" label is that group's
// derivative — but only when the shorter base owns a camera-native
// master and the labeled one does not. A labeled group with its own
// master is a separate photo, not an edit (IMG_01.NEF beside IMG.NEF),
// and where several shorter bases qualify the longest wins. Only groups
// in one directory are compared; a key that carries no directory is a
// not-yet-named file beside the working directory, and compares there.
//
// The merge honors the media boundary too: an edit is a derivative of
// the photo it was made from, so …-Edit.tif never merges into a video
// group, whatever base name the clip carries.
//
// The rule needs no exception for canonically named groups: every prefix
// is the same width, so no prefix can extend another, and a labeled
// derivative of a named master already shares its prefix.
func labeledMerge(candidates map[bucket][]string) map[bucket]bucket {
	native := make(map[bucket]bool, len(candidates))
	for id, paths := range candidates {
		native[id] = slices.ContainsFunc(paths, func(path string) bool {
			return identity.CameraNative(filepath.Ext(path))
		})
	}

	merged := make(map[bucket]bucket, len(candidates))
	for id := range candidates {
		merged[id] = id
		if native[id] {
			continue
		}
		dir, base := filepath.Dir(id.key), filepath.Base(id.key)
		parent := ""
		for other := range candidates {
			candidate := filepath.Base(other.key)
			if other.kind != id.kind || !native[other] || filepath.Dir(other.key) != dir {
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
			merged[id] = bucket{key: filepath.Join(dir, parent), kind: id.kind}
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
