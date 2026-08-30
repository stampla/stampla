package identity

import "time"

// Year-first, sortable timestamps only: YYYY MM DD then HH MM SS, each
// part with one consistent separator out of - . _ (or none), joined by
// at most one of T, space, - or _. Day-first or month-first forms
// (31.12.2016, 12/31/2016) are never interpreted: month and day are
// indistinguishable across locales, and a wrong-but-plausible date is
// worse than none. A trailing 3-digit block right after compact seconds
// is accepted as milliseconds (phone cameras) and ignored.
const (
	dateSeparators  = "-._"
	dateTimeJoiners = "T _-"
	minStampLength  = 4 + 2 + 2 + 2 + 2 + 2
)

// TimestampFromName recovers a capture time from a filename. Only
// complete, year-first timestamps qualify — a bare date has no time and
// would fabricate midnight.
//
// This is not part of any default chain and never fires on its own: a
// caller that wants the filename ranked injects the result under
// NameTimestampTag and uses a chain that lists it. A name is evidence
// about what a previous tool believed, not about the file's content.
func TimestampFromName(name string) (time.Time, bool) {
	for i := 0; i+minStampLength <= len(name); i++ {
		if i > 0 && isDigit(name[i-1]) {
			continue
		}
		found, ok := matchStamp(name, i)
		if !ok {
			continue
		}
		if found.hasMillis && found.timeSep != 0 {
			return time.Time{}, false // digits after separated seconds are not milliseconds
		}
		return validDate(found.year, found.month, found.day,
			found.hour, found.minute, found.second)
	}
	return time.Time{}, false
}

type nameStamp struct {
	year, month, day     int
	hour, minute, second int
	timeSep              byte
	hasMillis            bool
}

// matchStamp matches a timestamp anchored at index i. The optional
// separators are tried greedily and then empty, so that a name with
// inconsistent separators (2016-1231_101112) fails rather than
// half-matching.
func matchStamp(s string, i int) (nameStamp, bool) {
	if s[i:i+2] != "19" && s[i:i+2] != "20" {
		return nameStamp{}, false
	}
	year, ok := number(s, i, 4)
	if !ok {
		return nameStamp{}, false
	}
	for _, dateSep := range separators(s, i+4, dateSeparators) {
		at := i + 4 + width(dateSep)
		month, ok := number(s, at, 2)
		if !ok || month < 1 || month > 12 {
			continue
		}
		at += 2
		if dateSep != 0 {
			if at >= len(s) || s[at] != dateSep {
				continue
			}
			at++
		}
		day, ok := number(s, at, 2)
		if !ok || day < 1 || day > 31 {
			continue
		}
		at += 2
		if stamp, ok := matchTime(s, at, year, month, day); ok {
			return stamp, true
		}
	}
	return nameStamp{}, false
}

func matchTime(s string, start, year, month, day int) (nameStamp, bool) {
	for _, joiner := range separators(s, start, dateTimeJoiners) {
		at := start + width(joiner)
		hour, ok := number(s, at, 2)
		if !ok || hour > 23 {
			continue
		}
		at += 2
		for _, timeSep := range separators(s, at, dateSeparators) {
			cursor := at + width(timeSep)
			minute, ok := number(s, cursor, 2)
			if !ok || minute > 59 {
				continue
			}
			cursor += 2
			if timeSep != 0 {
				if cursor >= len(s) || s[cursor] != timeSep {
					continue
				}
				cursor++
			}
			second, ok := number(s, cursor, 2)
			if !ok || second > 59 {
				continue
			}
			cursor += 2
			for _, millis := range []int{3, 0} {
				if millis == 3 {
					if _, ok := number(s, cursor, 3); !ok {
						continue
					}
				}
				end := cursor + millis
				if end < len(s) && isDigit(s[end]) {
					continue // more digits: not a timestamp, whatever it is
				}
				return nameStamp{
					year: year, month: month, day: day,
					hour: hour, minute: minute, second: second,
					timeSep: timeSep, hasMillis: millis == 3,
				}, true
			}
		}
	}
	return nameStamp{}, false
}

// separators lists what an optional single-character separator at index
// i may be, greedy first: the character itself when it is one of the
// allowed set, then absent.
func separators(s string, i int, allowed string) []byte {
	if i < len(s) {
		for j := range len(allowed) {
			if s[i] == allowed[j] {
				return []byte{s[i], 0}
			}
		}
	}
	return []byte{0}
}

func width(separator byte) int {
	if separator == 0 {
		return 0
	}
	return 1
}

// number reads exactly n digits at index i.
func number(s string, i, n int) (int, bool) {
	if i+n > len(s) {
		return 0, false
	}
	value := 0
	for j := i; j < i+n; j++ {
		if !isDigit(s[j]) {
			return 0, false
		}
		value = value*10 + int(s[j]-'0')
	}
	return value, true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
