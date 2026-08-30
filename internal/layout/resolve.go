package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Provenance strings for Resolution.Source. Marker rungs report the
// marker file's path instead.
const (
	SourceFlag    = "--layout"
	SourceConfig  = "global config"
	SourceDefault = "built-in default"
)

// ConfigDirName and ConfigFileName locate the global config under the
// user's configuration directory.
const (
	ConfigDirName  = "stampla"
	ConfigFileName = "config"
)

// ErrContainer is returned by Resolve when the destination is a
// container: a directory declaring layout-for-children and no layout
// of its own. Containers hold archives, so files converge into a
// subdirectory, never into the container itself.
var ErrContainer = errors.New("destination is a container, not an archive")

// ContainerError names the container that refused the destination. It
// carries the marker so the caller can report the path and the layout
// a new archive underneath would be born with.
type ContainerError struct {
	Marker *Marker
}

func (e *ContainerError) Error() string {
	return fmt.Sprintf("%s: %v (it declares %s only)", e.Marker.Path(), ErrContainer, KeyLayoutForChildren)
}

func (e *ContainerError) Unwrap() error { return ErrContainer }

// Resolution is a resolved layout together with where it came from.
// Every report prints both.
type Resolution struct {
	// Pattern is the layout that governs the destination.
	Pattern Pattern
	// Source is the provenance, ready to print: SourceFlag, a marker
	// file's absolute path, SourceConfig, or SourceDefault.
	Source string
	// SourcePath is the file the layout was read from — a marker or
	// the global config — and empty for the flag and the default.
	SourcePath string
	// Declared is true only when the layout was chosen for this
	// destination: an explicit --layout, or the destination's own
	// marker. A fallback may place new files; only a declared layout
	// may relocate files that are already under the root.
	Declared bool
	// Marker is the destination's own marker, nil when it has none.
	// It is reported whichever rung won, and also alongside
	// ErrContainer.
	Marker *Marker
	// Warnings collects what every file consulted did not understand.
	Warnings []string
}

// Resolve decides which layout governs dest, in order:
//
//  1. flagLayout, when supplied,
//  2. dest's own marker layout,
//  3. the nearest ancestor marker declaring layout-for-children,
//  4. the global config's layout key,
//  5. DefaultPattern.
//
// An empty flagLayout means "not supplied"; use ResolveFlag to pass an
// explicit --layout "" (the flat layout).
//
// Before any of that, a destination whose own marker makes it a
// container is refused with ErrContainer — a container's nature is not
// something --layout overrides. The returned Resolution still carries
// the Marker and Warnings so the caller can explain the refusal.
//
// Inheritance from a container is a snapshot: Resolve reports the
// pattern and says where it came from, and the caller decides whether
// to record it in the new archive's own marker. Nothing here
// propagates a later edit to archives already born.
func Resolve(dest, flagLayout string) (Resolution, error) {
	if flagLayout == "" {
		return ResolveFlag(dest, nil)
	}
	return ResolveFlag(dest, &flagLayout)
}

// ResolveFlag is Resolve with the command-line flag passed as an
// option: nil when --layout was not given, and a pointer to "" when it
// was given as the flat layout.
func ResolveFlag(dest string, flagLayout *string) (Resolution, error) {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolving layout: %w", err)
	}

	var res Resolution
	own, err := ReadMarker(abs)
	if err != nil {
		return res, err
	}
	res.Marker = own
	if own != nil {
		res.Warnings = append(res.Warnings, own.Warnings()...)
		if own.IsContainer() {
			return res, &ContainerError{Marker: own}
		}
	}

	if flagLayout != nil {
		p, err := ParsePattern(*flagLayout)
		if err != nil {
			return res, fmt.Errorf("%s: %w", SourceFlag, err)
		}
		res.Pattern, res.Source, res.Declared = p, SourceFlag, true
		return res, nil
	}

	if own != nil && own.HasLayout() {
		p, err := ParsePattern(own.Layout)
		if err != nil {
			return res, fmt.Errorf("%s: %s: %w", own.Path(), KeyLayout, err)
		}
		res.Pattern, res.Source, res.SourcePath, res.Declared = p, own.Path(), own.Path(), true
		return res, nil
	}

	inherited, err := inherit(abs, &res)
	if err != nil {
		return res, err
	}
	if inherited {
		return res, nil
	}

	found, err := configLayout(&res)
	if err != nil {
		return res, err
	}
	if found {
		return res, nil
	}

	res.Pattern, res.Source = defaultPattern, SourceDefault
	return res, nil
}

// defaultPattern is the parsed DefaultPattern, the chain's last rung.
var defaultPattern = MustParsePattern(DefaultPattern)

// inherit walks abs's ancestors for the nearest container marker.
//
// The walk stops at an archive root: a marker declaring a layout of
// its own and nothing for children is a boundary, because a directory
// inside an archive is governed by that archive, not by whatever
// container sits above it. Callers detect that case with NearestRoot.
func inherit(abs string, res *Resolution) (bool, error) {
	for dir := parentDir(abs); dir != ""; dir = parentDir(dir) {
		m, err := ReadMarker(dir)
		if err != nil {
			return false, err
		}
		if m == nil {
			continue
		}
		res.Warnings = append(res.Warnings, m.Warnings()...)
		if m.HasLayoutForChildren() {
			p, err := ParsePattern(m.LayoutForChildren)
			if err != nil {
				return false, fmt.Errorf("%s: %s: %w", m.Path(), KeyLayoutForChildren, err)
			}
			res.Pattern, res.Source, res.SourcePath = p, m.Path(), m.Path()
			return true, nil
		}
		if m.HasLayout() {
			return false, nil
		}
	}
	return false, nil
}

// configLayout reads the layout key from the user's global config.
func configLayout(res *Resolution) (bool, error) {
	path, err := ConfigPath()
	if err != nil {
		// No configuration directory at all (no HOME): this rung does
		// not exist on this machine, which is absence, not failure.
		return false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading global config: %w", err)
	}
	values, _, warnings := parseSettings(path, []string{KeyLayout}, data)
	res.Warnings = append(res.Warnings, warnings...)
	value, ok := values[KeyLayout]
	if !ok {
		return false, nil
	}
	p, err := ParsePattern(value)
	if err != nil {
		return false, fmt.Errorf("%s: %s: %w", path, KeyLayout, err)
	}
	res.Pattern, res.Source, res.SourcePath = p, SourceConfig, path
	return true, nil
}

// ConfigPath is the global config file's path, under the user's
// configuration directory ($XDG_CONFIG_HOME/stampla/config and its
// per-platform equivalents).
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigDirName, ConfigFileName), nil
}

// NearestRoot returns the marker of the innermost archive dir belongs
// to, or nil, nil when it belongs to none.
//
// The walk is INCLUSIVE of dir: a directory that is itself an archive
// root is its own nearest root. Callers use it to notice that a
// destination sits inside an existing archive, and to stop a mutation
// scan when it reaches a nested root.
//
// Only markers declaring a layout count. A container is not an
// archive, so the walk passes through it and keeps climbing. Parents
// are taken lexically, without resolving symlinks, and the walk ends
// at the filesystem root (or a Windows drive or share root).
func NearestRoot(dir string) (*Marker, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("finding archive root: %w", err)
	}
	for d := abs; d != ""; d = parentDir(d) {
		m, err := ReadMarker(d)
		if err != nil {
			return nil, err
		}
		if m != nil && m.HasLayout() {
			return m, nil
		}
	}
	return nil, nil
}

// parentDir is dir's parent, or "" when dir is a root and has none.
// filepath.Dir is its own fixed point at "/", at "C:\" and at a UNC
// share root, which is what ends every upward walk in this package.
func parentDir(dir string) string {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ""
	}
	return parent
}
