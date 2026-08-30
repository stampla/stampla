package layout

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeMarker puts raw marker content in dir and returns dir.
func writeMarker(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readMarkerFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustRead(t *testing.T, dir string) *Marker {
	t.Helper()
	m, err := ReadMarker(dir)
	if err != nil {
		t.Fatalf("ReadMarker(%q) = %v", dir, err)
	}
	if m == nil {
		t.Fatalf("ReadMarker(%q) = nil, want a marker", dir)
	}
	return m
}

func TestReadMarkerAbsent(t *testing.T) {
	m, err := ReadMarker(t.TempDir())
	if err != nil {
		t.Fatalf("ReadMarker = %v, want no error", err)
	}
	if m != nil {
		t.Errorf("ReadMarker = %+v, want nil for a directory with no marker", m)
	}
}

func TestReadMarkerMissingDirectory(t *testing.T) {
	m, err := ReadMarker(filepath.Join(t.TempDir(), "nope"))
	if err != nil || m != nil {
		t.Errorf("ReadMarker = %v, %v; want nil, nil for a missing directory", m, err)
	}
}

func TestReadMarkerKeys(t *testing.T) {
	tests := []struct {
		name              string
		content           string
		layout            string
		hasLayout         bool
		layoutForChildren string
		hasForChildren    bool
		dam               string
		isContainer       bool
	}{
		{
			name:      "archive",
			content:   "layout = \"{yyyy}/{yyyy}-{mm}\"\n",
			layout:    "{yyyy}/{yyyy}-{mm}",
			hasLayout: true,
		},
		{
			name:              "container",
			content:           "layout-for-children = \"{yyyy}\"\n",
			layoutForChildren: "{yyyy}",
			hasForChildren:    true,
			isContainer:       true,
		},
		{
			name:              "archive that also seeds children",
			content:           "layout = \"Capture\"\nlayout-for-children = \"{yyyy}\"\n",
			layout:            "Capture",
			hasLayout:         true,
			layoutForChildren: "{yyyy}",
			hasForChildren:    true,
		},
		{
			name:      "dam",
			content:   "layout = \"{yyyy}\"\ndam = \"lrc\"\n",
			layout:    "{yyyy}",
			hasLayout: true,
			dam:       "lrc",
		},
		{
			name:      "flat layout is declared",
			content:   "layout = \"\"\n",
			layout:    "",
			hasLayout: true,
		},
		{
			name:           "flat layout for children",
			content:        "layout-for-children = \"\"\n",
			hasForChildren: true,
			isContainer:    true,
		},
		{
			name:      "whitespace and comments",
			content:   "# an archive\n\n   layout   =   \"{yyyy}\"   \n\n",
			layout:    "{yyyy}",
			hasLayout: true,
		},
		{
			name:      "crlf line endings",
			content:   "# windows wrote this\r\nlayout = \"{yyyy}\"\r\ndam = \"lrc\"\r\n",
			layout:    "{yyyy}",
			hasLayout: true,
			dam:       "lrc",
		},
		{
			name:      "empty file declares nothing",
			content:   "",
			hasLayout: false,
		},
		{
			name:      "comments only",
			content:   "# nothing here yet\n",
			hasLayout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeMarker(t, t.TempDir(), tt.content)
			m := mustRead(t, dir)
			if m.Layout != tt.layout {
				t.Errorf("Layout = %q, want %q", m.Layout, tt.layout)
			}
			if m.HasLayout() != tt.hasLayout {
				t.Errorf("HasLayout() = %v, want %v", m.HasLayout(), tt.hasLayout)
			}
			if m.LayoutForChildren != tt.layoutForChildren {
				t.Errorf("LayoutForChildren = %q, want %q", m.LayoutForChildren, tt.layoutForChildren)
			}
			if m.HasLayoutForChildren() != tt.hasForChildren {
				t.Errorf("HasLayoutForChildren() = %v, want %v", m.HasLayoutForChildren(), tt.hasForChildren)
			}
			if m.DAM != tt.dam {
				t.Errorf("DAM = %q, want %q", m.DAM, tt.dam)
			}
			if m.IsContainer() != tt.isContainer {
				t.Errorf("IsContainer() = %v, want %v", m.IsContainer(), tt.isContainer)
			}
			if m.Dir != dir {
				t.Errorf("Dir = %q, want %q", m.Dir, dir)
			}
			if want := filepath.Join(dir, MarkerName); m.Path() != want {
				t.Errorf("Path() = %q, want %q", m.Path(), want)
			}
			if got := m.Warnings(); len(got) != 0 {
				t.Errorf("Warnings() = %v, want none", got)
			}
		})
	}
}

func TestMarkerWarnings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "unknown key",
			content: "layout = \"{yyyy}\"\ncolour = \"blue\"\n",
			want:    []string{`:2: unknown key "colour"`},
		},
		{
			name:    "unparsable line",
			content: "layout = \"{yyyy}\"\nthis is not a setting\n",
			want:    []string{":2: unrecognized line"},
		},
		{
			name:    "unquoted value",
			content: "layout = {yyyy}\n",
			want:    []string{":1: unrecognized line"},
		},
		{
			name:    "repeated key",
			content: "layout = \"{yyyy}\"\nlayout = \"{mm}\"\n",
			want:    []string{`:2: repeated key "layout"`},
		},
		{
			name:    "several",
			content: "junk\nlayout = \"{yyyy}\"\nfuture-key = \"1\"\n",
			want:    []string{":1: unrecognized line", `:3: unknown key "future-key"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeMarker(t, t.TempDir(), tt.content)
			m := mustRead(t, dir)
			got := m.Warnings()
			if len(got) != len(tt.want) {
				t.Fatalf("Warnings() = %v, want %d warning(s)", got, len(tt.want))
			}
			for i, want := range tt.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("Warnings()[%d] = %q, want it to mention %q", i, got[i], want)
				}
				if !strings.HasPrefix(got[i], m.Path()) {
					t.Errorf("Warnings()[%d] = %q, want it to start with the marker path %q", i, got[i], m.Path())
				}
			}
		})
	}
}

func TestMarkerRepeatedKeyLastWins(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"\nlayout = \"{mm}\"\n")
	m := mustRead(t, dir)
	if m.Layout != "{mm}" {
		t.Errorf("Layout = %q, want the last value %q", m.Layout, "{mm}")
	}
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	if got, want := readMarkerFile(t, dir), "layout = \"{mm}\"\n"; got != want {
		t.Errorf("rewritten marker = %q, want the repeat collapsed to %q", got, want)
	}
}

func TestMarkerRoundTripPreservesEverythingElse(t *testing.T) {
	const content = `# Nikon imports, set up 2026-02-11
# do not move this file

layout = "{yyyy}/{yyyy}-{mm}"
colour = "blue"

  # trailing thoughts
dam = "lrc"
future-key = "keep me"
not even a setting
`
	dir := writeMarker(t, t.TempDir(), content)
	m := mustRead(t, dir)
	if err := m.Write(); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if got := readMarkerFile(t, dir); got != content {
		t.Errorf("round trip changed the file:\n got: %q\nwant: %q", got, content)
	}
	// And again: rewriting is stable, not merely reversible once.
	m2 := mustRead(t, dir)
	m2.SetLayout("Capture")
	if err := m2.Write(); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(content, `layout = "{yyyy}/{yyyy}-{mm}"`, `layout = "Capture"`, 1)
	if got := readMarkerFile(t, dir); got != want {
		t.Errorf("edited marker:\n got: %q\nwant: %q", got, want)
	}
	if warnings := mustRead(t, dir).Warnings(); len(warnings) != 3 {
		t.Errorf("Warnings() = %v, want 3 (unknown key, unknown key, unparsable line)", warnings)
	}
}

func TestMarkerWriteNew(t *testing.T) {
	tests := []struct {
		name  string
		build func(m *Marker)
		want  string
	}{
		{
			name:  "layout only",
			build: func(m *Marker) { m.Layout = "{yyyy}" },
			want:  "layout = \"{yyyy}\"\n",
		},
		{
			name:  "flat layout needs the setter",
			build: func(m *Marker) { m.SetLayout("") },
			want:  "layout = \"\"\n",
		},
		{
			name: "keys in a fixed order",
			build: func(m *Marker) {
				m.SetDAM("lrc")
				m.SetLayoutForChildren("{yyyy}")
				m.SetLayout("Capture")
			},
			want: "layout = \"Capture\"\nlayout-for-children = \"{yyyy}\"\ndam = \"lrc\"\n",
		},
		{
			name:  "nothing declared",
			build: func(_ *Marker) {},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			m := &Marker{Dir: dir}
			tt.build(m)
			if err := m.Write(); err != nil {
				t.Fatalf("Write = %v", err)
			}
			if got := readMarkerFile(t, dir); got != tt.want {
				t.Errorf("marker = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarkerWriteAppendsNewKeys(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "# mine\ndam = \"lrc\"\n")
	m := mustRead(t, dir)
	m.SetLayout("{yyyy}")
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	// dam keeps its place; layout is appended rather than reordered.
	if got, want := readMarkerFile(t, dir), "# mine\ndam = \"lrc\"\nlayout = \"{yyyy}\"\n"; got != want {
		t.Errorf("marker = %q, want %q", got, want)
	}
}

func TestMarkerWriteNormalizesLineEndings(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "# from windows\r\nlayout = \"{yyyy}\"\r\n")
	m := mustRead(t, dir)
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	got := readMarkerFile(t, dir)
	if strings.Contains(got, "\r") {
		t.Errorf("marker = %q, want carriage returns normalized away", got)
	}
	if want := "# from windows\nlayout = \"{yyyy}\"\n"; got != want {
		t.Errorf("marker = %q, want %q", got, want)
	}
}

func TestMarkerWriteMissingFinalNewline(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"")
	m := mustRead(t, dir)
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	if got, want := readMarkerFile(t, dir), "layout = \"{yyyy}\"\n"; got != want {
		t.Errorf("marker = %q, want %q", got, want)
	}
}

func TestMarkerWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	m := &Marker{Dir: dir, Layout: "{yyyy}"}
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(); err != nil {
		t.Fatalf("rewrite = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != MarkerName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory holds %v, want only %q — no temporary file left behind", names, MarkerName)
	}
}

func TestMarkerWritePreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	dir := writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"\n")
	path := filepath.Join(dir, MarkerName)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	m := mustRead(t, dir)
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %v, want the existing 0640 preserved", got)
	}
}

func TestMarkerWriteNewIsReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	dir := t.TempDir()
	m := &Marker{Dir: dir, Layout: "{yyyy}"}
	if err := m.Write(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, MarkerName))
	if err != nil {
		t.Fatal(err)
	}
	// A marker travels with the files; it must not inherit the 0600
	// of the temporary file it was written as.
	if got := info.Mode().Perm() & 0o044; got == 0 {
		t.Errorf("mode = %v, want a marker others can read", info.Mode().Perm())
	}
}

func TestMarkerWriteRejectsUnwritableValue(t *testing.T) {
	dir := t.TempDir()
	m := &Marker{Dir: dir, DAM: `lr"c`}
	err := m.Write()
	if err == nil {
		t.Fatal("Write = nil, want an error for a value holding a quote")
	}
	if !strings.Contains(err.Error(), "dam") {
		t.Errorf("error = %q, want it to name the key", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, MarkerName)); statErr == nil {
		t.Error("a marker file was created despite the error")
	}
}

func TestMarkerWriteWithoutDirectory(t *testing.T) {
	m := &Marker{Layout: "{yyyy}"}
	if err := m.Write(); err == nil {
		t.Fatal("Write = nil, want an error when Dir is empty")
	}
}

func TestReadMarkerUnreadable(t *testing.T) {
	dir := t.TempDir()
	// A marker that is a directory cannot be read; that is an error,
	// not an absent marker.
	if err := os.Mkdir(filepath.Join(dir, MarkerName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMarker(dir); err == nil {
		t.Error("ReadMarker = nil error, want a failure for an unreadable marker")
	}
}

func TestParseSetting(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		key   string
		value string
		ok    bool
	}{
		{name: "plain", line: `layout = "{yyyy}"`, key: "layout", value: "{yyyy}", ok: true},
		{name: "no spaces", line: `dam="lrc"`, key: "dam", value: "lrc", ok: true},
		{name: "hyphen and digits in key", line: `layout-for-children = "x"`, key: "layout-for-children", value: "x", ok: true},
		{name: "key with a digit", line: `layout2 = "x"`, key: "layout2", value: "x", ok: true},
		{name: "empty value", line: `layout = ""`, key: "layout", value: "", ok: true},
		{name: "value with spaces", line: `layout = "My Photos/{yyyy}"`, key: "layout", value: "My Photos/{yyyy}", ok: true},
		{name: "equals in the value", line: `layout = "a=b"`, key: "layout", value: "a=b", ok: true},
		{name: "no equals sign", line: `layout "{yyyy}"`},
		{name: "unquoted value", line: `layout = {yyyy}`},
		{name: "half quoted", line: `layout = "{yyyy}`},
		{name: "quote inside the value", line: `layout = "a"b"`},
		{name: "empty key", line: `= "x"`},
		{name: "uppercase key", line: `Layout = "x"`},
		{name: "key starting with a digit", line: `9lives = "x"`},
		{name: "key starting with a hyphen", line: `-layout = "x"`},
		{name: "underscore in key", line: `layout_for = "x"`},
		{name: "trailing junk", line: `layout = "x" and more`},
		{name: "empty line", line: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value, ok := parseSetting(tt.line)
			if ok != tt.ok {
				t.Fatalf("parseSetting(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if ok && (key != tt.key || value != tt.value) {
				t.Errorf("parseSetting(%q) = %q, %q; want %q, %q", tt.line, key, value, tt.key, tt.value)
			}
		})
	}
}

func TestMarkerHasDAM(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "dam = \"lrc\"\n")
	if m := mustRead(t, dir); !m.HasDAM() || m.DAM != "lrc" {
		t.Errorf("HasDAM() = %v, DAM = %q; want true, %q", m.HasDAM(), m.DAM, "lrc")
	}
	if m := mustRead(t, writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"\n")); m.HasDAM() {
		t.Error("HasDAM() = true, want false when the key is absent")
	}
}

func TestLongUnrecognizedLineIsTruncated(t *testing.T) {
	long := strings.Repeat("ą", 200) // multi-byte: truncation must not split a rune
	dir := writeMarker(t, t.TempDir(), long+"\n")
	warnings := mustRead(t, dir).Warnings()
	if len(warnings) != 1 {
		t.Fatalf("Warnings() = %v, want 1", warnings)
	}
	if len(warnings[0]) > len(dir)+200 {
		t.Errorf("warning is %d bytes, want the quoted line truncated", len(warnings[0]))
	}
	if !strings.Contains(warnings[0], "…") {
		t.Errorf("warning = %q, want it to show the truncation", warnings[0])
	}
	if !utf8.ValidString(warnings[0]) {
		t.Errorf("warning = %q, want valid UTF-8 after truncation", warnings[0])
	}
}

func TestMarkerWarningsAreACopy(t *testing.T) {
	dir := writeMarker(t, t.TempDir(), "junk line\n")
	m := mustRead(t, dir)
	got := m.Warnings()
	if len(got) != 1 {
		t.Fatalf("Warnings() = %v, want 1", got)
	}
	got[0] = "tampered"
	if again := m.Warnings(); again[0] == "tampered" {
		t.Error("Warnings() handed out its own slice")
	}
}
