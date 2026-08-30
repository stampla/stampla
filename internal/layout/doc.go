// Package layout owns directory organization: layout patterns, the
// .stampla marker that declares them, and the chain that resolves
// which layout governs a destination.
//
// The filename is the identity; the path is organization. A layout
// pattern maps a capture time to a relative directory — date tokens
// ({yyyy}, {mm}, {dd}, {yyyy-mm}, {yyyy-mm-dd}) and literal text in
// segments joined with '/', e.g. "{yyyy}/{yyyy}-{mm}", "Capture", or
// "" (flat). Every token is a pure function of the capture time, so
// the correct directory for a named file is derivable from its name
// alone; that is what makes a misplaced file detectable. Patterns
// that could not render a portable path — unknown tokens, absolute
// paths, "..", empty segments, characters Windows forbids — are
// refused at parse time rather than at placement time.
//
// # The marker
//
// A .stampla file at an archive root is plain text, one
// key = "value" per line, with # comments and blank lines:
//
//	# Nikon imports
//	layout = "{yyyy}/{yyyy}-{mm}"
//	dam = "lrc"
//
// layout declares an archive. layout-for-children declares a
// container: a directory that holds archives, whose children inherit
// that layout at birth. dam names a digital asset manager that must
// perform its own renames. Anything else in the file — unknown keys,
// comments, unparsable lines — is preserved verbatim and in place
// when the marker is rewritten, and reported through Marker.Warnings.
// The marker travels with the files, so a disk carried to another
// machine behaves identically.
//
// # Resolution
//
// Resolve answers "which layout governs this destination", in order:
// an explicit --layout, the destination's own marker, the nearest
// ancestor container marker, the user's global config, then
// DefaultPattern. Every resolution reports its provenance, and
// whether the layout was Declared for this destination — a fallback
// may place new files, but only a declared layout may reorganize
// files already under the root.
//
// Inheritance from a container is a snapshot: Resolve reports the
// inherited pattern and where it came from, and the caller decides
// whether to record it in the new archive's own marker. Editing a
// container never re-shapes archives already born beneath it.
package layout
