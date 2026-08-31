package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/finding"
)

// TestPorcelainGolden pins the machine interface line for line. These
// are the exact bytes another program is built against, so a change here
// is a change to format 1 and has to be a new format instead.
func TestPorcelainGolden(t *testing.T) {
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})

	res := declaredIn(testDest, pattern)
	imported := filepath.Join(testDest, "2026", "2026-07", "b.nef")
	plan := fakePlan(engine.Copy,
		converged(archiveFile),
		incoming(cardFile, imported),
	)
	p.head(engine.Copy, testDest, false, res)
	p.progress(engine.PhaseRead, 0, 2, cardFile)
	p.body(archive{root: testDest, mode: engine.Copy, res: res, plan: plan})
	p.tail(outcome{
		exit: finding.ExitConverged, applied: 1, failed: 0,
		receipt: testReceipt, marker: testMarker,
	})

	// The paths are quoted the way the stream has to quote them on this
	// platform — a Windows separator is a JSON escape — so the golden
	// stays an exact, whole-line comparison wherever it runs.
	dest, marker := jsonString(t, testDest), jsonString(t, testMarker)
	want := []string{
		`{"type":"plan","format":1,"mode":"cp","dest":` + dest +
			`,"layout":"{yyyy}/{yyyy}-{mm}","source":` + marker + `}`,
		`{"type":"progress","phase":"read","done":0,"total":2}`,
		`{"type":"finding","class":"converged","path":` + jsonString(t, archiveFile) +
			`,"old":` + jsonString(t, archiveFile) +
			`,"new":"","detail":"name, hash and location all match"}`,
		`{"type":"finding","class":"incoming","path":` + jsonString(t, cardFile) +
			`,"old":` + jsonString(t, cardFile) + `,"new":` + jsonString(t, imported) +
			`,"detail":"named from EXIF:DateTimeOriginal"}`,
		`{"type":"result","exit":0,"applied":1,"failed":0,"receipt":` +
			jsonString(t, testReceipt) + `,"marker":` + marker + `}`,
	}
	got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("the stream has %d lines, want %d:\n%s", len(got), len(want), stdout.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i+1, got[i], want[i])
		}
	}
}

// TestPorcelainDoesNotEscapePaths proves a path is written as the
// filesystem spells it. HTML escaping would leave a consumer reading
// \u0026 where there is an ampersand.
func TestPorcelainDoesNotEscapePaths(t *testing.T) {
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})
	ampersand := filepath.Join(testDest, "Tom & Ann", "a.jpg")
	p.body(archive{plan: fakePlan(engine.Copy, converged(ampersand))})

	line := strings.TrimSuffix(stdout.String(), "\n")
	if strings.Contains(line, `\u0026`) {
		t.Errorf("the ampersand was escaped as HTML: %s", line)
	}
	if got := parsedPath(t, line); got != ampersand {
		t.Errorf("the path parsed back as %q, want %q", got, ampersand)
	}
}

// TestPorcelainEncodesSeparators proves a path full of backslashes
// leaves the stream as valid JSON that parses back to the path itself.
// Escaping is encoding/json's, not this package's, and this is the test
// that would notice if it ever stopped being.
func TestPorcelainEncodesSeparators(t *testing.T) {
	const windowsPath = `C:\photos\2026\20260703_150727_0a8c8109.jpg`
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})
	p.body(archive{plan: fakePlan(engine.Copy, converged(windowsPath))})

	line := strings.TrimSuffix(stdout.String(), "\n")
	if !strings.Contains(line, `\\photos`) {
		t.Errorf("a separator reached the stream unescaped: %s", line)
	}
	if got := parsedPath(t, line); got != windowsPath {
		t.Errorf("the path parsed back as %q, want %q", got, windowsPath)
	}
}

// TestPorcelainEveryFieldIsPresent proves the shape does not vary with
// the data: a consumer parses one shape, always.
func TestPorcelainEveryFieldIsPresent(t *testing.T) {
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})
	p.head(engine.VerifySelf, testDest, false, declaredIn(testDest, ""))
	p.body(archive{plan: fakePlan(engine.VerifySelf,
		finding.Finding{Class: finding.Missing, Path: filepath.Join(testCard, "x.nef")})})
	p.tail(outcome{exit: finding.ExitPending})

	fields := map[string][]string{
		"plan":    {"type", "format", "mode", "dest", "layout", "source"},
		"finding": {"type", "class", "path", "old", "new", "detail"},
		"result":  {"type", "exit", "applied", "failed", "receipt", "marker"},
	}
	for _, line := range strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("unparsable line %q: %v", line, err)
		}
		kind, _ := event["type"].(string)
		want, known := fields[kind]
		if !known {
			t.Fatalf("unknown event type in %q", line)
		}
		if len(event) != len(want) {
			t.Errorf("%s carries %d fields, want %d: %s", kind, len(event), len(want), line)
		}
		for _, name := range want {
			if _, present := event[name]; !present {
				t.Errorf("%s is missing %q: %s", kind, name, line)
			}
		}
	}
}

// TestPorcelainOnePlanPerArchive proves a descent into nested roots says
// which archive, under which declaration, each finding belongs to.
func TestPorcelainOnePlanPerArchive(t *testing.T) {
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})
	nested := filepath.Join(testDest, "2019")
	p.head(engine.VerifySelf, testDest, false, declaredIn(testDest, pattern))
	p.head(engine.VerifySelf, nested, true, declaredIn(nested, "Capture"))
	p.tail(outcome{})

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the stream has %d lines, want 3:\n%s", len(lines), stdout.String())
	}
	if !strings.Contains(lines[1], `"dest":`+jsonString(t, nested)) ||
		!strings.Contains(lines[1], `"layout":"Capture"`) {
		t.Errorf("the nested archive's plan does not name it: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], `{"type":"result"`) {
		t.Errorf("the last line is not the result envelope: %s", lines[2])
	}
}

// parsedPath is one finding line's path, read back the way a consumer
// reads it: through a JSON parser, which is what makes an assertion
// about a path hold whatever separators the platform spells it with.
func parsedPath(t *testing.T, line string) string {
	t.Helper()
	var event struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("unparsable line %q: %v", line, err)
	}
	return event.Path
}

// jsonString is a value as the stream spells it, quotes and escapes
// included, so an expectation is built by the same rule the stream is.
// It escapes HTML, which the stream deliberately does not: a golden line
// holding a path with an ampersand in it belongs in the test above.
func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("quoting %q: %v", value, err)
	}
	return string(encoded)
}
