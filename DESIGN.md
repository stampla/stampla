# Stampla design

Stampla gives every photo and video a name derived purely from the
file itself, and converges archives toward that naming. This document
is the specification the implementation follows. Its principles are
ordered; when they conflict, the earlier one wins.

1. **Never destroy information.** No overwrite, ever. A source is
   deleted only after its copy is verified. Original filenames are
   recorded in receipts before they are replaced. Evidence of damage
   is preserved, never renamed away.
2. **The file is the source of truth.** Identity is a pure function
   of file content and metadata. Anything the tool knows must be
   re-derivable from the files — the two deliberate exceptions
   (declared directory layout, receipts) live beside the files in
   plain text and travel with them.
3. **Explain everything.** Every action is previewable, every report
   states its evidence and provenance, every refusal names its
   reason. Deterministic behavior only: same input state, same plan.

## Identity

The canonical name of a file decomposes as
`<prefix>[<suffix>][.<raw_ext>].<ext>`:

```
20260703_150727_9b677b64.nef            a master
20260703_150727_9b677b64-Edit.tif       a labeled derivative (suffix)
20260703_150727_9b677b64.nef.xmp        a sidecar appending to the
                                        master's extension (raw_ext)
```

Only the prefix ever changes when a file is renamed; suffix, raw
extension and extension are always preserved. All files sharing a
prefix form a group. The prefix is:

```
YYYYMMDD_HHMMSS_hhhhhhhh
```

- `YYYYMMDD_HHMMSS` — capture time in local wall-clock time, as a
  person at the scene would have read off a watch, resolved from
  metadata by tag-group-qualified ranking (below). Timezone suffixes
  in metadata values are deliberately ignored; the wall-clock part is
  the identity.
- `hhhhhhhh` — the first 8 hex digits of the file's image-data hash
  (ExifTool `ImageDataHash`, MD5): the hash of the image/video
  payload only, excluding metadata. Writing metadata never changes a
  file's identity; editing image data does. For a media format whose
  payload ExifTool cannot isolate, the whole-file MD5 stands in and
  the report says so — such files trade the metadata-edit tolerance
  for full coverage.
- `ext` — the original extension, lowercased.

The scheme is fixed. It is versioned as a whole (a future scheme
change is a migration, not a setting); nothing about it is
configurable. Because the name encodes both halves of the identity,
a file whose recomputed identity matches its name is verified —
with no database involved. A random payload corruption escapes the
8-digit slice with probability 2⁻³².

### Capture-time resolution

Several tags with the same short name routinely coexist in one file
(a maker-notes wall-clock time next to a QuickTime UTC time), and
only the tag-group-qualified name tells them apart. Times are read
with `-a` and full group names, then ranked: maker/EXIF original
capture tags first, then format-appropriate creation tags, with
QuickTime UTC values converted to local time via the declared or
inferred offset. A file with no resolvable capture time is a finding
(`unresolvable`), never a guess — a silently wrong date is the
failure mode this tool exists to prevent.

### Groups

A master and its sidecars (e.g. `.xmp`, vendor sidecar directories)
form one group sharing the name prefix. Groups converge atomically:
selecting any member selects the group; a group either fully
converges or is reported, and a failure mid-group is healed by
re-running (convergence is idempotent).

## The engine

There is one operation: **converge files onto their identities under
a destination root**. All commands are entry points to it.

```
stampla cp <inputs...> <dest>    converge by copying in
stampla mv <inputs...> <dest>    converge by moving / renaming in place
stampla verify <src> <dest>      report: is src accounted for in dest?
stampla verify <dest>            report: does dest converge with itself?
```

- Inputs: files are taken literally, directories recurse. `--stdin`
  reads a newline-delimited list from standard input (`-z`:
  NUL-delimited, for `find -print0`); with `--stdin` the sole
  positional is the destination.
- The destination must be an existing directory.
- `verify` never mutates and descends into nested archive roots,
  applying each root's own declaration. `cp` and `mv` stop at nested
  roots — another archive inside this one is not this archive's
  business.
- `cp`/`mv` act immediately; `-n`/`--dry-run` previews the exact
  plan. `mv` on a file already under the destination is a rename or
  relocation in place. `mv` from another volume is per-group:
  copy → fsync → re-hash and verify at the destination → only then
  delete the source.
- Membership (`verify <src> <dest>`) computes each source file's
  expected path under the destination's layout and checks it —
  "accounted for" means present at its place in **this** archive.
  Exit 0 means every source file is; for a memory card, that it is
  safe to format.

### Classes

Every file lands in exactly one class per run:

| class | meaning | mutation verbs |
|---|---|---|
| `converged` | name, hash and location all match | skip |
| `misplaced` | correct name, wrong directory per declared layout | relocate (declared layouts only) |
| `stale` | recomputed identity differs on an editable format | rename |
| `corrupt` | payload hash differs on a write-once format | **refuse + alarm** |
| `time-drift` | capture time differs on a write-once format, hash intact | **refuse + alarm** |
| `unresolvable` | no capture time derivable | report |
| `conflict` | target path already occupied by different content | refuse + report |
| `missing` | (membership check) not present at expected path | report |

A write-once format (RAW, camera-original video) is never renamed on
a mismatch: the old name is the only surviving record of what the
file's identity used to be, and renaming would convert damage into a
plausible file. Editable formats (JPEG, TIFF, DNG, HEIC and
sidecars) drift legitimately and rename. `time-drift` is separate
from `corrupt` because ImageDataHash does not cover metadata — a
damaged metadata region must not masquerade as an innocent date
edit.

## Layout

The filename is the identity; the directory path is organization.
Layout is declared per destination and resolved in order:

1. `--layout` on the command line,
2. the destination's own `.stampla` file,
3. the nearest ancestor `.stampla` declaring `layout-for-children`,
4. the user's global config (`$XDG_CONFIG_HOME/stampla/config`),
5. the built-in default `{yyyy}/{yyyy}-{mm}`.

Patterns are date tokens plus literal segments: `{yyyy}`, `{mm}`,
`{dd}`, `{yyyy-mm}`, `{yyyy-mm-dd}`, e.g. `{yyyy}/{yyyy}-{mm}`,
`Capture`, or the empty pattern `""` (flat). Every report prints the
resolved layout and where it came from.

### The `.stampla` marker

A short plain-text file of `key = "value"` lines at an archive root:

- `layout = "…"` — this directory is an archive with this layout.
  Written automatically after every successful `cp`/`mv`, recording
  the layout that actually shaped the tree.
- `layout-for-children = "…"` — this directory is a **container**:
  new archives created beneath it inherit this layout at birth
  (a snapshot — later edits never re-shape existing children). A
  container is not an archive; converging into it is an error.
- `dam = "lrc"` — this archive's masters are managed by a DAM that
  must perform its own renames; `mv` refuses here.

Unknown keys are preserved and warned about. Markers travel with the
files; a disk carried to another machine behaves identically.

### Placement rules

- Files **entering** a root are placed by the resolved layout.
- Files **already under** a root are relocated only when the layout
  is *declared* — by the root's own marker or an explicit `--layout`.
  A fallback default may place new files; it may never reorganize an
  existing structure. On an undeclared root, names still converge
  and a hint suggests declaring.

## Confirmations

Certain shapes of operation pause for an interactive confirmation.
A confirmation never changes the plan — it gates it. The triggers
are fixed, documented predicates:

1. an explicit `--layout` contradicts the destination's recorded
   marker;
2. the plan would rename or relocate more than 100 files **and**
   more than half the files already under the root;
3. DAM artifacts (a Lightroom catalog, a Capture One session) sit
   beside the target of an `mv`;
4. `mv` whose source is removable media (the card is the last-resort
   backup until it is deliberately formatted).

Prompts read from the controlling terminal, not stdin (stdin may be
a `--stdin` file list). Without a terminal the operation is refused
with a hint; `-y`/`--yes` skips all confirmations. A declined or
refused confirmation is its own exit code, distinct from success and
from findings.

## Durability

- Every write lands under a temporary name in the destination
  directory and is atomically renamed into place; a partial file
  never exists under an identity name.
- Every applied mutation appends one line to the receipt
  (`.stampla.log` beside the marker): RFC 3339 time, verb, old path,
  new path, tab-separated. The receipt is the permanent record of
  original filenames — never destroy information — and is
  human-readable by design.
- Crash recovery is re-running the same command: converged work
  skips, interrupted groups complete. There is no journal, no undo
  command, and no state beyond the marker and the receipt.

## Interface contract

- Exit codes: `0` converged/verified · `1` findings that need action
  (stale, misplaced, missing, unresolvable, conflict) · `2` alarms
  (corrupt, time-drift) or operational trouble · `3` confirmation
  declined or refused · `64` usage error. Alarms dominate findings.
- Report on stdout; progress on stderr only when it is a terminal.
- `--porcelain` emits NDJSON (`format: 1`): one object per finding
  (`{"type":"finding","class":…,"path":…,"old":…,"new":…}`) with
  progress events interleaved and one final result envelope. This is
  the single machine interface, for scripts and GUIs alike.
- `--color=auto|always|never` (default auto, honors `NO_COLOR`);
  `--` ends option parsing; no pager.

## ExifTool

ExifTool is the only external dependency, and deliberately so: no
native library reads capture time across RAW and video formats with
comparable coverage, and none replicates `ImageDataHash`. It runs as
persistent `-stay_open` processes; bulk reads shard file lists
across a small pool so per-file startup cost is never paid. Metadata
is always requested with `-a` and tag-group-qualified names.
