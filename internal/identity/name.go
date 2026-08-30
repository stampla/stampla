package identity

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// stampLayout is the capture-time half of a canonical name. Fixed-width
// tokens, most significant first: that ordering is the promise the
// archive is built on, that sorting names sorts by capture time.
const stampLayout = "20060102_150405"

const (
	// HashLength is how many hex digits of the image-data hash a
	// canonical name carries. Eight digits let a random payload
	// corruption escape the check with probability 2^-32.
	HashLength = 8

	stampLength = len("20060102_150405")
	// prefixLength covers stamp, separator and hash slice.
	prefixLength = stampLength + 1 + HashLength
)

// Identity is what a file's name says about the file: when it was
// captured and what it contains, plus the name parts a rename must
// preserve. A canonical name decomposes as
//
//	<prefix>[<suffix>][.<rawext>].<ext>
//
// Only the prefix — Time and Hash — ever changes when a group is
// renamed. Suffix labels a derivative (-Edit, _pr) and RawExt is the
// master's extension on sidecars from tools that append rather than
// replace (.nef.xmp, .rw2.pp3); both are load-bearing for grouping and
// are carried through untouched.
type Identity struct {
	// Time is naive local wall-clock capture time; see the package docs
	// on the carrier location.
	Time time.Time
	// Hash is the leading HashLength hex digits of the image-data hash.
	Hash string
	// Ext is the file's own extension, lowercase, without the dot.
	Ext string
	// Suffix is a derivative label starting with "-" or "_", or "".
	Suffix string
	// RawExt is the master extension an appended sidecar keeps, or "".
	RawExt string
}

// Prefix is the identity proper: the part every member of a group
// shares and the only part a rename rewrites.
func (id Identity) Prefix() string {
	return id.Time.Format(stampLayout) + "_" + id.Hash
}

// Name is the canonical filename for this identity.
func (id Identity) Name() string {
	name := id.Prefix() + id.Suffix
	if id.RawExt != "" {
		name += "." + id.RawExt
	}
	return name + "." + id.Ext
}

// IsMaster reports whether this name is the group's hash-carrying
// member: a media file with no derivative label and no appended master
// extension. Everything else in a group inherits its prefix.
func (id Identity) IsMaster() bool {
	return id.Suffix == "" && id.RawExt == "" && isMediaExt(id.Ext)
}

// canonicalName accepts exactly what Name produces. An uppercase
// extension or hash slice is not canonical: such a file still needs a
// rename, so reporting it as already named would hide work.
var canonicalName = regexp.MustCompile(
	`^(\d{8}_\d{6})_([0-9a-f]{8})([-_][^.]*)?(?:\.(` + rawExtAlternation() + `))?\.([a-z0-9]+)$`)

// canonicalPrefix is the identity part alone, used to recognize members
// of a group whose names carry a suffix, a master extension or both.
var canonicalPrefix = regexp.MustCompile(`^\d{8}_\d{6}_[0-9a-f]{8}$`)

// rawExtAlternation lists the extensions a sidecar may append to,
// longest first so that ".tiff.xmp" never settles for "tif".
func rawExtAlternation() string {
	extensions := slices.Clone(rawExtensions)
	slices.SortFunc(extensions, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return strings.Join(extensions, "|")
}

// ParseName decomposes a canonical filename. The second result is false
// for anything else, including a grammar-valid name encoding an
// impossible date (20260231_…): such a name is not an identity, and a
// name that cannot be re-derived must not pass as one.
func ParseName(base string) (Identity, bool) {
	match := canonicalName.FindStringSubmatch(base)
	if match == nil {
		return Identity{}, false
	}
	stamp, err := time.Parse(stampLayout, match[1])
	if err != nil {
		return Identity{}, false
	}
	return Identity{
		Time:   stamp,
		Hash:   match[2],
		Suffix: match[3],
		RawExt: match[4],
		Ext:    match[5],
	}, true
}

// namedPrefix reports the canonical prefix a filename starts with. The
// prefix must be a whole token: junk may not precede it, and what
// follows must start a suffix, an extension or nothing at all, so that
// a longer hash slice never half-matches this one. A name whose tail
// breaks the grammar still groups with its master — selecting any
// member selects the group — even though ParseName refuses it.
func namedPrefix(base string) (string, bool) {
	if len(base) < prefixLength {
		return "", false
	}
	prefix := base[:prefixLength]
	if !canonicalPrefix.MatchString(prefix) {
		return "", false
	}
	if len(base) > prefixLength && !strings.ContainsRune("-_.", rune(base[prefixLength])) {
		return "", false
	}
	return prefix, true
}
