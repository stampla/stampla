// Package layout owns directory organization: layout patterns, the
// .stampla destination marker, and the resolution chain that decides
// which layout governs a destination.
//
// The filename is the identity; the path is organization. A layout
// pattern maps a capture time to a relative directory: date tokens
// ({yyyy}, {mm}, {dd}, {yyyy-mm}, {yyyy-mm-dd}) joined with literal
// segments, e.g. "{yyyy}/{yyyy}-{mm}", "Capture", or "" (flat).
//
// The .stampla marker is a short plain-text file of key = "value"
// lines at an archive root. layout declares an archive; container
// markers declare layout-for-children, inherited by new roots at
// birth (a snapshot, never live); dam names a DAM that must perform
// its own renames. Unknown keys are preserved on rewrite.
//
// Resolution order: --layout flag > the destination's own marker >
// nearest ancestor container marker > global config
// ($XDG_CONFIG_HOME/stampla/config, key layout) > built-in default
// "{yyyy}/{yyyy}-{mm}". Every resolution records its source for the
// report's provenance line.
//
// Required API:
//
//	type Pattern struct{ /* opaque */ }
//	func ParsePattern(s string) (Pattern, error)
//	func (p Pattern) Dir(t time.Time) string // relative, "" for flat
//	func (p Pattern) String() string
//
//	type Marker struct {
//	    Dir               string // directory holding the marker
//	    Layout            string // "" if absent
//	    LayoutForChildren string // "" if absent
//	    DAM               string // "" if absent
//	    // unknown lines preserved verbatim for rewrite
//	}
//	func ReadMarker(dir string) (*Marker, error) // nil, nil when absent
//	func (m *Marker) Write() error               // atomic, preserves unknown lines
//
//	type Resolution struct {
//	    Pattern  Pattern
//	    Source   string // e.g. `--layout`, `Catalog/.stampla`, `built-in default`
//	    Declared bool   // marker or explicit flag (may relocate in place)
//	    Marker   *Marker // the destination's own marker, if any
//	}
//	func Resolve(dest, flagLayout string) (Resolution, error)
//
//	// NearestRoot walks parents from dir looking for a marker;
//	// used to refuse converging into a container, to redirect when
//	// the destination sits inside an archive, and to stop mutation
//	// scans at nested roots.
//	func NearestRoot(dir string) (*Marker, error) // nil, nil when none
package layout
