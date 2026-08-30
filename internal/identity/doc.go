// Package identity computes and parses canonical names, resolves
// capture times, and owns all format knowledge: which extensions are
// media, which formats are write-once, and what makes a sidecar.
// Everything here is a pure function of its inputs — no I/O — so the
// hard rules are exhaustively table-testable.
//
// # Names
//
// The canonical name is YYYYMMDD_HHMMSS_hhhhhhhh.ext: capture time in
// local wall-clock time, the first 8 hex digits of the ImageDataHash,
// and the lowercased original extension. The datetime tokens run
// most-significant first and every field is fixed width, which is what
// makes sorting names sort by capture time. ParseName is strict — it
// accepts exactly what Name produces, so Name and ParseName round-trip.
// Sidecars and labeled derivatives (20260214_125556_1355acb2-Edit.tif,
// …_1355acb2.nef.xmp) are not identities of their own; they belong to
// their master's group and are recognized by GroupKey, not ParseName.
//
// # Capture time
//
// Resolve tries an ordered chain of tag names against the
// tag-group-qualified metadata ExifTool returned. The first entry that
// yields a complete, plausible timestamp wins. A file no entry matches
// is unresolvable — reported, never guessed at, never given a partial
// date. Chain entries come in two forms:
//
//   - "Group:Tag" matches exactly that group.
//   - "Tag" matches the tag in any group, except groups that some entry
//     of the same chain names explicitly for that tag. This encodes
//     rankings such as "any CreateDate, but QuickTime's only as a last
//     resort": list CreateDate first and QuickTime:CreateDate later.
//     QuickTime timestamps are usually UTC while maker-notes ones are
//     local wall-clock, so the local source must win when both are
//     present. Eligible groups are tried in name order, and the
//     Composite group last of all: ExifTool computes those values from
//     the other tags after extraction, so a group that was actually
//     read wins whenever one answers, and Composite still wins when it
//     is the only candidate. An explicit "Composite:Tag" entry matches
//     like any other qualified entry.
//
// An entry suffixed "@utc" declares that the tag stores UTC: its value
// is converted, DST-aware, into the machine's local zone, and the
// resolution's source carries the marker so reports always show that a
// conversion happened. Neither default chain marks an entry: no format
// stores UTC there reliably (BRAW writes local wall-clock into the
// QuickTime atoms and offers nothing else), so QuickTime is ranked last
// and taken at face value instead of converted on a guess.
//
// All resolved values are naive local wall-clock time, as a person at
// the scene would have read off a watch. Timezone suffixes in metadata
// values are deliberately ignored; the wall-clock part is the identity.
// The returned time.Time carries time.UTC as a neutral carrier for
// those wall-clock fields, never as a claim about the zone.
//
// # Groups
//
// A master and its sidecars converge as one unit, keyed by GroupKey.
// Named files key on their name prefix alone: prefixes embed a content
// hash, so they are unique per master across the whole archive and
// sidecars kept in subdirectories (NKSC_PARAM/<master>.nksc) fall into
// their master's group with no directory logic at all. Files not yet
// named key on their directory and original base name, so DSC1234.NEF,
// DSC1234.xmp and DSC1234.NEF.xmp share one key.
//
// One grouping rule is deliberately not here, because it is a property
// of a set of files rather than of one path: a group whose base extends
// another group's base with a "-" or "_" label (DSC1234-Edit) merges
// into that group only when the shorter base owns a master and the
// labeled group does not. A labeled group with its own master file is a
// separate photo, not a derivative, so IMG_01.NEF never merges into
// IMG.NEF; where several shorter bases qualify, the longest wins. The
// engine applies that merge over the keys GroupKey returns, using
// camera-native master extensions (not tif or dng) so that editor
// output like …-Edit.tif counts as a derivative rather than a master.
package identity
