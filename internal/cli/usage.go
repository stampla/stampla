package cli

// The usage texts. They are written out rather than generated from the
// flag sets: a flag's one-line description and the example that shows
// what the verb is for are two different pieces of writing, and only one
// of them can be derived.
const rootUsage = `stampla stamps every photo and video with its own identity: when it was
captured and what it contains.

usage:
  stampla cp <inputs...> <dest>       copy files into an archive under their identity names
  stampla mv <inputs...> <dest>       move files into place; also heals an archive in place
  stampla verify <src> <dest>         is everything in src accounted for in dest? exit 0 = yes
  stampla verify <dest>               check an archive: names, placement, integrity
  stampla version                     the version of stampla and of ExifTool
  stampla help [verb]                 this, or one verb in full

exit: 0 converged  1 findings that need action  2 alarms or trouble
      3 confirmation declined  64 usage
`

const copyUsage = `usage: stampla cp [-n] [-y] [--layout P] [--stdin [-z]] [--porcelain]
                  [--color=MODE] [--workers N] <inputs...> <dest>

Copy files into an archive under their identity names. Sources are never
modified and never deleted. Files are taken literally; directories
recurse and stop at nested archive roots.

  -n, --dry-run     preview the plan and write nothing
  -y, --yes         answer every confirmation yes
      --layout P    the destination's layout ("" is flat), overriding its marker
      --stdin       read the input list from standard input; <dest> is the only argument
  -z                that list is NUL-delimited, for find -print0
      --porcelain   NDJSON on stdout (format 1) instead of a report
      --color=MODE  auto, always or never (default auto, honors NO_COLOR)
      --workers N   cap the ExifTool and hashing pools (default: one per CPU)

example:
  stampla cp /Volumes/NIKON/DCIM /photos
`

const moveUsage = `usage: stampla mv [-n] [-y] [--layout P] [--stdin [-z]] [--porcelain]
                  [--color=MODE] [--workers N] <inputs...> <dest>

Move files into place, and heal an archive in place. A source is deleted
only after its copy has been re-read and verified at the destination. A
file already under the destination is renamed or relocated where it
stands.

  -n, --dry-run     preview the plan and write nothing
  -y, --yes         answer every confirmation yes
      --layout P    the destination's layout ("" is flat), overriding its marker
      --stdin       read the input list from standard input; <dest> is the only argument
  -z                that list is NUL-delimited, for find -print0
      --porcelain   NDJSON on stdout (format 1) instead of a report
      --color=MODE  auto, always or never (default auto, honors NO_COLOR)
      --workers N   cap the ExifTool and hashing pools (default: one per CPU)

example:
  stampla mv /photos/inbox /photos
`

const verifyUsage = `usage: stampla verify [--porcelain] [--color=MODE] [--workers N] <src> <dest>
       stampla verify [--porcelain] [--color=MODE] [--workers N] <dest>

With two arguments, ask whether every file in src is accounted for at its
place in the archive at dest: exit 0 means it is, and a memory card is
safe to format. With one, check the archive against itself — names,
placement and integrity — descending into nested archive roots and
applying each root's own declaration.

verify never writes anything, and takes neither -n nor --layout: what
governs an archive is what the archive declares.

      --porcelain   NDJSON on stdout (format 1) instead of a report
      --color=MODE  auto, always or never (default auto, honors NO_COLOR)
      --workers N   cap the ExifTool and hashing pools (default: one per CPU)

example:
  stampla verify /Volumes/NIKON/DCIM /photos
`

// usageFor is a verb's usage text, and whether there is such a verb.
func usageFor(verb string) (string, bool) {
	switch verb {
	case verbCopy:
		return copyUsage, true
	case verbMove:
		return moveUsage, true
	case verbVerify:
		return verifyUsage, true
	case "help", "version":
		return rootUsage, true
	default:
		return "", false
	}
}
