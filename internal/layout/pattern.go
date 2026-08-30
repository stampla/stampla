package layout

import (
	"fmt"
	"strings"
	"time"
)

// DefaultPattern is the built-in layout, the last rung of the
// resolution chain: a year directory holding year-month directories.
const DefaultPattern = "{yyyy}/{yyyy}-{mm}"

// tokenHelp lists the legal tokens for error messages, in the order
// the documentation introduces them.
const tokenHelp = "{yyyy}, {mm}, {dd}, {yyyy-mm}, {yyyy-mm-dd}"

// renderers is the closed set of date tokens. A directory must be
// derivable from a capture time alone — that is what makes a
// misplaced file detectable — so every token is a pure function of
// that time and nothing else. There is deliberately no token for
// information chosen at import time.
var renderers = map[string]func(time.Time) string{
	"yyyy":       func(t time.Time) string { return fmt.Sprintf("%04d", t.Year()) },
	"mm":         func(t time.Time) string { return fmt.Sprintf("%02d", int(t.Month())) },
	"dd":         func(t time.Time) string { return fmt.Sprintf("%02d", t.Day()) },
	"yyyy-mm":    func(t time.Time) string { return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month())) },
	"yyyy-mm-dd": func(t time.Time) string { return fmt.Sprintf("%04d-%02d-%02d", t.Year(), int(t.Month()), t.Day()) },
}

// sampleTime renders a pattern at parse time so segment shape can be
// checked once, deterministically, instead of at every placement.
var sampleTime = time.Date(2006, time.January, 2, 15, 4, 5, 0, time.UTC)

// reservedNames are device names Windows refuses as path components,
// with or without an extension.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// badChars are the characters no portable path component may contain.
// '/' is the segment separator and '\' is normalized to it before
// this check runs.
const badChars = `<>:"|?*`

// part is one piece of a path segment: a literal run of text, or a
// date token named by token (literal is then empty).
type part struct {
	literal string
	token   string
}

// segment is one path component: literals and tokens in source order.
type segment []part

func (seg segment) render(t time.Time) string {
	var b strings.Builder
	for _, p := range seg {
		if p.token == "" {
			b.WriteString(p.literal)
			continue
		}
		b.WriteString(renderers[p.token](t))
	}
	return b.String()
}

// Pattern is a parsed layout: the relative directory a capture time
// maps to. The zero Pattern is the flat layout, which places every
// file directly in the archive root.
//
// Patterns are not comparable with ==; compare String values.
type Pattern struct {
	raw  string
	segs []segment
}

// ParsePattern parses a layout pattern: date tokens ({yyyy}, {mm},
// {dd}, {yyyy-mm}, {yyyy-mm-dd}) and literal text, in segments joined
// with '/'. The empty string is the flat layout.
//
// A pattern must render a relative path that is legal on every
// supported platform, so ParsePattern rejects unknown tokens,
// absolute paths, empty segments, "." and "..", characters Windows
// forbids, Windows device names, and segments that would end in a
// space or a dot. A pattern that parses here renders a usable
// directory for every capture time.
//
// '\' is accepted as a separator and normalized to '/' — a Windows
// user typing the pattern means the same tree — so it never reaches
// the parsed form.
func ParsePattern(s string) (Pattern, error) {
	norm := strings.ReplaceAll(s, `\`, "/")
	if norm == "" {
		return Pattern{}, nil
	}
	if strings.HasPrefix(norm, "/") || hasDriveLetter(norm) {
		return Pattern{}, invalidf(s, "must be a relative path")
	}
	p := Pattern{raw: norm}
	for _, raw := range strings.Split(norm, "/") {
		if raw == "" {
			return Pattern{}, invalidf(s, "empty path segment")
		}
		seg, err := parseSegment(raw)
		if err != nil {
			return Pattern{}, invalidf(s, "%v", err)
		}
		if err := checkComponent(seg.render(sampleTime)); err != nil {
			return Pattern{}, invalidf(s, "%v", err)
		}
		p.segs = append(p.segs, seg)
	}
	return p, nil
}

// MustParsePattern parses a pattern known to be valid, and panics if
// it is not. It is for package-level constants such as DefaultPattern.
func MustParsePattern(s string) Pattern {
	p, err := ParsePattern(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Dir renders the relative directory for a capture time, always with
// '/' separators; callers join it onto a root with filepath.Join. The
// flat layout renders "".
//
// The time is used as given: capture times are local wall-clock times
// already, and Dir never converts between zones.
func (p Pattern) Dir(t time.Time) string {
	if len(p.segs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.segs))
	for _, seg := range p.segs {
		parts = append(parts, seg.render(t))
	}
	return strings.Join(parts, "/")
}

// String returns the pattern as written, normalized to '/'
// separators. The flat layout is "".
func (p Pattern) String() string { return p.raw }

// IsFlat reports whether the pattern places files directly in the
// archive root.
func (p Pattern) IsFlat() bool { return len(p.segs) == 0 }

// parseSegment splits one path component into literals and tokens.
func parseSegment(raw string) (segment, error) {
	var seg segment
	for rest := raw; rest != ""; {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			if err := checkLiteral(rest); err != nil {
				return nil, err
			}
			return append(seg, part{literal: rest}), nil
		}
		if open > 0 {
			lit := rest[:open]
			if err := checkLiteral(lit); err != nil {
				return nil, err
			}
			seg = append(seg, part{literal: lit})
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			return nil, fmt.Errorf("unterminated token %q", rest[open:])
		}
		name := rest[open+1 : open+closing]
		if _, ok := renderers[name]; !ok {
			return nil, fmt.Errorf("unknown token %q (known tokens: %s)", "{"+name+"}", tokenHelp)
		}
		seg = append(seg, part{token: name})
		rest = rest[open+closing+1:]
	}
	return seg, nil
}

// checkLiteral rejects characters that cannot appear in a portable
// path component. Tokens are exempt: they render digits and dashes.
func checkLiteral(s string) error {
	for _, r := range s {
		switch {
		case r == '}':
			return fmt.Errorf("unmatched %q", "}")
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("invalid control character %q", r)
		case strings.ContainsRune(badChars, r):
			return fmt.Errorf("invalid character %q (a layout must render paths that work on Windows too)", r)
		}
	}
	return nil
}

// checkComponent rejects rendered path components that are not usable
// as a directory name.
func checkComponent(s string) error {
	switch s {
	case ".":
		return fmt.Errorf("path segment %q is not a directory name", s)
	case "..":
		return fmt.Errorf("path segment %q would leave the archive", s)
	}
	if last := s[len(s)-1]; last == ' ' || last == '.' {
		return fmt.Errorf("path segment %q ends in %q (Windows silently strips it)", s, string(last))
	}
	stem, _, _ := strings.Cut(s, ".")
	if reservedNames[strings.ToUpper(stem)] {
		return fmt.Errorf("path segment %q is a reserved device name on Windows", s)
	}
	return nil
}

// hasDriveLetter reports whether s starts with a Windows drive
// specifier, which must be refused as an absolute path rather than
// reported as a stray ':'.
func hasDriveLetter(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func invalidf(pattern, format string, args ...any) error {
	return fmt.Errorf("invalid layout %q: %s", pattern, fmt.Sprintf(format, args...))
}
