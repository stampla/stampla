// Package scanner collects the files a run operates on and expands them
// into the groups that converge as one unit.
//
// # Inputs
//
// Explicit files are taken literally: a named file is collected whatever
// directory it sits in, and one that does not exist — or that stampla
// owns no identity for — becomes a finding rather than a silent skip.
// Directories recurse. Options.Stdin replaces the arguments with a list
// read from there, newline-delimited or, with Options.NulSep,
// NUL-delimited for find -print0.
//
// # Scan errors are findings
//
// A directory that cannot be listed holds files this run cannot see, and
// a report that omitted them would call an unreadable card safe to
// format — the failure mode the tool exists to prevent. Every such path
// lands in Scan.Errors and the walk continues, so one unreadable corner
// never hides the rest of the tree. Collect itself returns an error only
// when the input list cannot be read at all. Two classes carry the scan's
// own troubles:
//
//   - finding.Missing — a path the scan could not account for: an input
//     that does not exist, a directory that would not list, a marker that
//     would not read.
//   - finding.Unresolvable — an explicitly named file stampla can derive
//     no identity for: neither media nor a sidecar, or not a regular
//     file at all.
//
// # Filtering
//
// Recursion keeps media files and the sidecars that rename with them.
// Dotfiles and dot-directories — the .stampla marker among them — are
// skipped, as are files in formats the tool does not own; both are
// counted in Scan.Skipped so that "the scan saw nothing" is never
// mistaken for "there was nothing to see". Filtering applies to
// recursion only: an explicitly named file is never quietly dropped.
//
// Options.KeepUnowned turns the format filter off, collecting those
// files instead of counting them. The membership check sets it, because
// its exit code says whether a memory card may be formatted and a file
// the report never mentioned is a file that answer did not cover. Dot
// files are skipped either way.
//
// # Nested roots
//
// A directory whose .stampla marker declares a layout is another
// archive. With Options.StopAtRoots — the mutation verbs — recursion
// records it in Scan.NestedRoots and does not descend: another archive
// inside this one is not this run's business. The verify verb clears the
// option, records the same roots and descends anyway, so the caller can
// re-run each root under its own declaration. A marker that cannot be
// read is a finding, and a mutation scan does not descend past it — a
// run must never assume that unreadable evidence says "not an archive".
//
// # Groups
//
// A master and its sidecars and derivatives converge as one unit, keyed
// by identity.GroupKey. Group atomicity beats literal selection:
// selecting any member selects the group, so the scan examines each
// selected file's neighborhood on disk — its group's home directory and
// that directory's immediate subdirectories, which is where vendor
// sidecar directories live — and pulls unselected members in. A pulled-in
// member is marked Item.Implied, so a report can say why it appears.
//
// Over those keys the scan applies the one grouping rule that is a
// property of a set of files rather than of one path, described in
// identity's package docs: a group whose base extends another group's
// base with a "-" or "_" label merges into it, but only when the shorter
// base owns a camera-native master and the labeled group does not. A
// labeled group with its own master is a separate photo, not a
// derivative (IMG_01.NEF never merges into IMG.NEF), and where several
// shorter bases qualify the longest wins. Camera-native means
// identity.CameraNative: editor output such as …-Edit.tif counts as a
// derivative rather than as a master of its own.
//
// # The media boundary
//
// A group never spans photo and video. Same-base stills stay one group —
// a RAW and its JPEG twin are one capture — but a photo and a video
// sharing a base name (IMG_1234.HEIC beside IMG_1234.MOV, every Live
// Photo's shape) are two groups, each mastering itself. One name asserts
// one identity, and a still that took a clip's prefix would claim a
// capture it never had, in a name no later verify would question: only
// masters are content-checked, and the still would be a member.
//
// A sidecar joins the group its name names — DSC_1234.NEF.xmp the
// still's, DSC_1234.MP4.xmp the clip's. A bare DSC_1234.xmp names
// neither, and the longer claim wins: it joins the photo group wherever
// the base has one (bare sidecars are what photo tools write), and the
// video group only when there is none. Because two groups can share one
// key this way, Group.ID — key and kind — is what identifies a group;
// Group.Key names only the base its members are stemmed against.
//
// Groups and their members come back in a fixed order — by key, then
// photo before video, members master first and then sidecars and
// derivatives by path — so the same input state yields the same Scan.
package scanner
