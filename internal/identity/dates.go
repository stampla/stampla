package identity

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrNoCaptureTime reports that no chain entry yielded a usable capture
// time. It is the unresolvable class: the file is reported, never given
// a guessed or partial date.
var ErrNoCaptureTime = errors.New("no capture time")

// utcMarker suffixes a chain entry whose tag stores UTC, and the source
// of any resolution that converted one.
const utcMarker = "@utc"

// compositeGroup holds the values ExifTool computes from other tags
// after extraction rather than reads from the file. An unqualified
// chain entry reaches them only when no real group answers it; a chain
// that wants one names it explicitly.
const compositeGroup = "Composite"

// NameTimestampTag names the synthetic source of a timestamp recovered
// from a filename. It is not an ExifTool tag: a caller that wants the
// filename ranked injects it into the tags it passes and uses a chain
// that lists it. Neither default chain does — a name is not evidence
// about the file's content.
const NameTimestampTag = "File:NameTimestamp"

// PhotoChain ranks capture-time tags for still formats. EXIF's original
// capture tag first, then EXIF's creation tag, then the XMP date a
// sidecar or an editor leaves behind.
var PhotoChain = []string{
	"EXIF:DateTimeOriginal",
	"EXIF:CreateDate",
	"XMP:DateCreated",
}

// VideoChain ranks capture-time tags for moving formats. MakerNotes
// values are local wall-clock time; QuickTime values are usually UTC
// but some formats (BRAW) store local time there and offer nothing
// else, hence QuickTime last and taken at face value. The first two
// entries are unqualified so that any maker's group can serve them,
// while the explicit QuickTime entry keeps QuickTime out of both.
var VideoChain = []string{
	"DateTimeOriginal",
	"CreateDate",
	"QuickTime:CreateDate",
}

// DefaultChain resolves a file whose format is not known: the photo
// chain followed by the video chain. Because the entries an unqualified
// video entry would reach are exactly the ones the photo chain has
// already tried or explicitly deferred, this ranks every tag the same
// way the format-specific chains do.
var DefaultChain = slices.Concat(PhotoChain, VideoChain)

// ChainFor is the chain that ranks capture-time tags for a file with
// this extension (with or without its leading dot).
func ChainFor(ext string) []string {
	if isVideoExt(normalizeExt(ext)) {
		return VideoChain
	}
	return PhotoChain
}

// Resolve ranks the tag-group-qualified capture-time candidates of a
// file whose format is not known, returning the winning tag name as
// provenance. Prefer ResolveWith with ChainFor when the extension is at
// hand.
func Resolve(tags map[string]string) (time.Time, string, error) {
	return ResolveWith(tags, DefaultChain)
}

// ResolveWith resolves a capture time from tag-group-qualified tags
// using the given chain, returning the winning tag name as provenance.
// The error wraps ErrNoCaptureTime when nothing in the chain matched.
func ResolveWith(tags map[string]string, chain []string) (time.Time, string, error) {
	reserved := reservedGroups(chain)
	var keys []string     // sorted lazily; only unqualified entries need them
	var rejected []string // the evidence behind a refusal

	for _, raw := range chain {
		entry, isUTC := strings.CutSuffix(raw, utcMarker)
		if strings.Contains(entry, ":") {
			if stamp, ok := ParseDateTime(tags[entry]); ok {
				if isUTC {
					return utcToWallClock(stamp), entry + utcMarker, nil
				}
				return stamp, entry, nil
			}
			rejected = note(rejected, entry, tags[entry])
			continue
		}
		if keys == nil {
			keys = sortedKeys(tags)
		}
		// Unqualified entry: any group but the ones some entry of this
		// chain claims for the same tag. Candidates are taken in name
		// order, never in the order ExifTool happened to print them —
		// same input state, same answer — and the Composite group is
		// held back to a second pass, because those values are
		// ExifTool's own derivations of the others.
		for _, composite := range []bool{false, true} {
			for _, key := range keys {
				group, tag, qualified := strings.Cut(key, ":")
				if !qualified || tag != entry || reserved[entry][group] {
					continue
				}
				if (group == compositeGroup) != composite {
					continue
				}
				if stamp, ok := ParseDateTime(tags[key]); ok {
					if isUTC {
						return utcToWallClock(stamp), key + utcMarker, nil
					}
					return stamp, key, nil
				}
				rejected = note(rejected, key, tags[key])
			}
		}
	}

	return time.Time{}, "", unresolved(tags, chain, rejected)
}

// unresolved explains a refusal with the evidence a report needs: the
// values that were there and unusable, or the tags that were looked for
// and absent. Metadata is read with -All, so the tags a file happens to
// carry are never listed wholesale.
func unresolved(tags map[string]string, chain, rejected []string) error {
	switch {
	case len(tags) == 0:
		return fmt.Errorf("%w: no metadata tags present", ErrNoCaptureTime)
	case len(rejected) > 0:
		return fmt.Errorf("%w: no usable value in %s",
			ErrNoCaptureTime, strings.Join(rejected, ", "))
	default:
		return fmt.Errorf("%w: none of %s present (%d tags read)",
			ErrNoCaptureTime, strings.Join(chain, ", "), len(tags))
	}
}

// note records a tag whose value the chain looked at and refused. Only
// the first few are kept, and long values are cut: this ends up in a
// one-line report, not in a dump.
func note(rejected []string, key, value string) []string {
	const (
		maxNotes  = 4
		maxLength = 40
	)
	if value == "" || len(rejected) > maxNotes {
		return rejected
	}
	if len(rejected) == maxNotes {
		return append(rejected, "…")
	}
	if len(value) > maxLength {
		value = value[:maxLength] + "…"
	}
	return append(rejected, key+"="+strconv.Quote(value))
}

// reservedGroups maps a tag name to the groups the chain names
// explicitly for it, anywhere in the chain — an unqualified entry
// listed before them still defers to them.
func reservedGroups(chain []string) map[string]map[string]bool {
	reserved := make(map[string]map[string]bool)
	for _, raw := range chain {
		entry, _ := strings.CutSuffix(raw, utcMarker)
		group, tag, qualified := strings.Cut(entry, ":")
		if !qualified {
			continue
		}
		if reserved[tag] == nil {
			reserved[tag] = make(map[string]bool)
		}
		reserved[tag][group] = true
	}
	return reserved
}

func sortedKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// exifDateTime matches ExifTool's YYYY:mm:dd HH:MM:SS, unanchored at
// the end so that subseconds and timezone suffixes are simply dropped.
var exifDateTime = regexp.MustCompile(`^(\d{4}):(\d{2}):(\d{2})[ T](\d{2}):(\d{2}):(\d{2})`)

// ParseDateTime parses an ExifTool datetime value. Subseconds and
// timezone suffixes are dropped: the wall-clock part is the identity.
// Anything incomplete or implausible (zero dates, bare dates, an
// impossible day, garbage) is refused — a partial date must never
// become part of a filename.
func ParseDateTime(value string) (time.Time, bool) {
	match := exifDateTime.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, false
	}
	var parts [6]int
	for i, field := range match[1:] {
		parts[i], _ = number(field, 0, len(field))
	}
	return validDate(parts[0], parts[1], parts[2], parts[3], parts[4], parts[5])
}

// validDate builds a wall-clock time, refusing any field time.Date
// would silently normalize away (month 13, February 30, hour 88).
func validDate(year, month, day, hour, minute, second int) (time.Time, bool) {
	stamp := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if stamp.Year() != year || int(stamp.Month()) != month || stamp.Day() != day ||
		stamp.Hour() != hour || stamp.Minute() != minute || stamp.Second() != second {
		return time.Time{}, false
	}
	return stamp, true
}

// utcToWallClock reads a naive UTC timestamp as local wall-clock time,
// DST-aware, for sources that only store UTC.
func utcToWallClock(stamp time.Time) time.Time {
	local := stamp.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
}
