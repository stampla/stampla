package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MarkerName is the file that declares an archive root or a container.
// It lives in the directory it describes and travels with the files.
const MarkerName = ".stampla"

// The keys a marker may declare.
const (
	// KeyLayout declares this directory an archive with this layout.
	KeyLayout = "layout"
	// KeyLayoutForChildren declares this directory a container: new
	// archives created beneath it take this layout at birth.
	KeyLayoutForChildren = "layout-for-children"
	// KeyDAM names a digital asset manager that must perform its own
	// renames; mv refuses in such an archive.
	KeyDAM = "dam"
)

// markerKeys is the order keys are appended in when the file they are
// written to did not already carry a line for them.
var markerKeys = []string{KeyLayout, KeyLayoutForChildren, KeyDAM}

// srcLine is one line of a marker or config file. A line carrying a
// known key is regenerated from the current value on write; every
// other line — comments, blank lines, unknown keys, anything
// unparsable — is kept verbatim and in place. The file belongs to the
// user; the tool edits its own keys and nothing else.
type srcLine struct {
	key  string
	text string
}

// Marker is a parsed .stampla file.
//
// The empty layout is the flat layout, so an empty Layout field means
// either "absent" or "flat": HasLayout tells the two apart. Assigning
// a non-empty value to a field declares that key; declaring the flat
// layout on a marker built from scratch needs SetLayout("").
type Marker struct {
	// Dir is the directory holding the marker file.
	Dir string
	// Layout is the archive's own layout pattern.
	Layout string
	// LayoutForChildren is the layout new archives beneath this
	// directory inherit at birth.
	LayoutForChildren string
	// DAM names the digital asset manager owning this archive's
	// masters.
	DAM string

	declared map[string]bool
	lines    []srcLine
	warnings []string
}

// ReadMarker reads dir's marker. It returns nil, nil when the
// directory has none, which is not an error: most directories are not
// archives.
func ReadMarker(dir string) (*Marker, error) {
	path := filepath.Join(dir, MarkerName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading marker: %w", err)
	}
	values, lines, warnings := parseSettings(path, markerKeys, data)
	m := &Marker{
		Dir:      dir,
		declared: make(map[string]bool, len(values)),
		lines:    lines,
		warnings: warnings,
	}
	for key, value := range values {
		m.declared[key] = true
		switch key {
		case KeyLayout:
			m.Layout = value
		case KeyLayoutForChildren:
			m.LayoutForChildren = value
		case KeyDAM:
			m.DAM = value
		}
	}
	return m, nil
}

// Path is the marker file's path.
func (m *Marker) Path() string { return filepath.Join(m.Dir, MarkerName) }

// HasLayout reports whether the marker declares a layout — and so
// whether this directory is an archive root. It is true for the flat
// layout, which HasLayout exists to distinguish from an absent key.
func (m *Marker) HasLayout() bool { return m.has(KeyLayout) }

// HasLayoutForChildren reports whether the marker declares a layout
// for archives created beneath this directory.
func (m *Marker) HasLayoutForChildren() bool { return m.has(KeyLayoutForChildren) }

// HasDAM reports whether the marker names a digital asset manager.
func (m *Marker) HasDAM() bool { return m.has(KeyDAM) }

// IsContainer reports whether this directory only holds archives: it
// declares a layout for its children but none of its own. Converging
// files into a container is an error.
//
// A marker may declare both keys; that directory is an archive that
// also sets its children's default, not a container.
func (m *Marker) IsContainer() bool { return m.HasLayoutForChildren() && !m.HasLayout() }

// SetLayout declares the archive's layout, including the flat layout.
func (m *Marker) SetLayout(pattern string) { m.set(KeyLayout, pattern) }

// SetLayoutForChildren declares the layout new archives beneath this
// directory inherit at birth.
func (m *Marker) SetLayoutForChildren(pattern string) { m.set(KeyLayoutForChildren, pattern) }

// SetDAM names the digital asset manager owning this archive.
func (m *Marker) SetDAM(name string) { m.set(KeyDAM, name) }

// Warnings lists what the marker file said that this version of the
// tool does not understand: unknown keys, unparsable lines, repeated
// keys. Each is "<path>:<line>: <what>". Nothing is ever dropped
// because of a warning — the lines survive the next rewrite verbatim.
func (m *Marker) Warnings() []string {
	if len(m.warnings) == 0 {
		return nil
	}
	return append([]string(nil), m.warnings...)
}

// Write renders the marker and replaces the file atomically: a
// temporary file in the same directory, then a rename, so a marker is
// never observed half-written.
//
// Lines the tool does not own keep their content and their position;
// keys the file did not already carry are appended in a fixed order.
func (m *Marker) Write() error {
	if m.Dir == "" {
		return errors.New("marker: no directory to write to")
	}
	body, err := m.render()
	if err != nil {
		return fmt.Errorf("%s: %w", m.Path(), err)
	}
	if err := writeAtomic(m.Path(), body); err != nil {
		return fmt.Errorf("writing marker: %w", err)
	}
	return nil
}

func (m *Marker) render() ([]byte, error) {
	var b strings.Builder
	written := make(map[string]bool, len(markerKeys))
	for _, ln := range m.lines {
		if ln.key == "" {
			b.WriteString(ln.text)
			b.WriteByte('\n')
			continue
		}
		if !m.has(ln.key) {
			continue
		}
		setting, err := formatSetting(ln.key, m.field(ln.key))
		if err != nil {
			return nil, err
		}
		b.WriteString(setting)
		written[ln.key] = true
	}
	for _, key := range markerKeys {
		if written[key] || !m.has(key) {
			continue
		}
		setting, err := formatSetting(key, m.field(key))
		if err != nil {
			return nil, err
		}
		b.WriteString(setting)
	}
	return []byte(b.String()), nil
}

func (m *Marker) field(key string) string {
	switch key {
	case KeyLayout:
		return m.Layout
	case KeyLayoutForChildren:
		return m.LayoutForChildren
	case KeyDAM:
		return m.DAM
	default:
		return ""
	}
}

func (m *Marker) set(key, value string) {
	switch key {
	case KeyLayout:
		m.Layout = value
	case KeyLayoutForChildren:
		m.LayoutForChildren = value
	case KeyDAM:
		m.DAM = value
	}
	if m.declared == nil {
		m.declared = make(map[string]bool, len(markerKeys))
	}
	m.declared[key] = true
}

// has reports whether the key is declared: it was present in the file,
// it was set explicitly, or it holds a value. The last case is what
// makes a hand-built &Marker{Layout: "…"} work.
func (m *Marker) has(key string) bool { return m.declared[key] || m.field(key) != "" }

// parseSettings splits marker or config text into the values of the
// known keys, the lines to preserve, and warnings about everything
// else. The format is deliberately dumb: key = "value", # comments,
// blank lines. Both files use it.
func parseSettings(path string, known []string, data []byte) (map[string]string, []srcLine, []string) {
	isKnown := make(map[string]bool, len(known))
	for _, key := range known {
		isKnown[key] = true
	}
	values := make(map[string]string, len(known))
	var lines []srcLine
	var warnings []string
	warn := func(n int, format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf("%s:%d: %s", path, n, fmt.Sprintf(format, args...)))
	}

	for n, raw := range splitLines(data) {
		// A file written on Windows and read on Unix must behave
		// identically; the marker travels with the disk.
		text := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(text)
		key, value, ok := parseSetting(trimmed)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case !ok:
			warn(n+1, "unrecognized line %q, kept as it is", ellipsis(trimmed, 60))
		case !isKnown[key]:
			warn(n+1, "unknown key %q, kept as it is", key)
		default:
			if _, dup := values[key]; dup {
				warn(n+1, "repeated key %q, the last value wins", key)
				values[key] = value
				continue // the first line keeps the key's place in the file
			}
			values[key] = value
			lines = append(lines, srcLine{key: key})
			continue
		}
		lines = append(lines, srcLine{text: text})
	}
	return values, lines, warnings
}

// ellipsis shortens text quoted back to the user, so one absurd line
// in a marker cannot flood a report.
func ellipsis(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}

// splitLines splits file content into lines, without inventing a
// trailing blank line for the final newline.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// parseSetting parses one `key = "value"` line. ok is false for
// anything that is not exactly that shape, so unparsable input is
// preserved rather than half-understood.
func parseSetting(s string) (key, value string, ok bool) {
	name, rest, found := strings.Cut(s, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(name)
	if !validKey(key) {
		return "", "", false
	}
	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || !strings.HasPrefix(rest, `"`) || !strings.HasSuffix(rest, `"`) {
		return "", "", false
	}
	value = rest[1 : len(rest)-1]
	if strings.ContainsAny(value, "\"\r\n") {
		return "", "", false
	}
	return key, value, true
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r == '-' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}

func formatSetting(key, value string) (string, error) {
	if strings.ContainsAny(value, "\"\r\n") {
		return "", fmt.Errorf("%s: value %q cannot be written (quote or newline)", key, value)
	}
	return fmt.Sprintf("%s = %q\n", key, value), nil
}

// writeAtomic writes data to a temporary file in the destination's own
// directory and renames it into place, so no reader ever sees a
// partial file and no crash leaves one behind.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	perm := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	f, err := os.CreateTemp(dir, ".stampla-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err = writeAndClose(f, data, perm); err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(dir)
	return nil
}

func writeAndClose(f *os.File, data []byte, perm fs.FileMode) error {
	// A marker travels with the files; keep it readable rather than
	// at the 0600 os.CreateTemp gives. A filesystem without modes
	// costs nothing here.
	_ = f.Chmod(perm)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir flushes the rename itself. Directories cannot be synced
// everywhere, and failing to costs durability, not correctness.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
