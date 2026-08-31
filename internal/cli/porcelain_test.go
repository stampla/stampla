package cli

import (
	"encoding/json"
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

	res := declaredIn("/photos", pattern)
	plan := fakePlan(engine.Copy,
		converged("/photos/a.jpg"),
		incoming("/card/b.nef", "/photos/2026/2026-07/b.nef"),
	)
	p.head(engine.Copy, "/photos", false, res)
	p.progress(engine.PhaseRead, 0, 2, "/card/b.nef")
	p.body(archive{root: "/photos", mode: engine.Copy, res: res, plan: plan})
	p.tail(outcome{
		exit: finding.ExitConverged, applied: 1, failed: 0,
		receipt: "/photos/.stampla.log", marker: "/photos/.stampla",
	})

	want := []string{
		`{"type":"plan","format":1,"mode":"cp","dest":"/photos","layout":"{yyyy}/{yyyy}-{mm}","source":"/photos/.stampla"}`,
		`{"type":"progress","phase":"read","done":0,"total":2}`,
		`{"type":"finding","class":"converged","path":"/photos/a.jpg","old":"/photos/a.jpg","new":"","detail":"name, hash and location all match"}`,
		`{"type":"finding","class":"incoming","path":"/card/b.nef","old":"/card/b.nef","new":"/photos/2026/2026-07/b.nef","detail":"named from EXIF:DateTimeOriginal"}`,
		`{"type":"result","exit":0,"applied":1,"failed":0,"receipt":"/photos/.stampla.log","marker":"/photos/.stampla"}`,
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
	p.body(archive{plan: fakePlan(engine.Copy, converged("/photos/Tom & Ann/a.jpg"))})
	if !strings.Contains(stdout.String(), "/photos/Tom & Ann/a.jpg") {
		t.Errorf("the path was escaped: %s", stdout.String())
	}
}

// TestPorcelainEveryFieldIsPresent proves the shape does not vary with
// the data: a consumer parses one shape, always.
func TestPorcelainEveryFieldIsPresent(t *testing.T) {
	var stdout strings.Builder
	p := newPorcelain(&out{w: &stdout})
	p.head(engine.VerifySelf, "/photos", false, declaredIn("/photos", ""))
	p.body(archive{plan: fakePlan(engine.VerifySelf,
		finding.Finding{Class: finding.Missing, Path: "/card/x.nef"})})
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
	p.head(engine.VerifySelf, "/photos", false, declaredIn("/photos", pattern))
	p.head(engine.VerifySelf, "/photos/2019", true, declaredIn("/photos/2019", "Capture"))
	p.tail(outcome{})

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the stream has %d lines, want 3:\n%s", len(lines), stdout.String())
	}
	if !strings.Contains(lines[1], `"dest":"/photos/2019"`) ||
		!strings.Contains(lines[1], `"layout":"Capture"`) {
		t.Errorf("the nested archive's plan does not name it: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], `{"type":"result"`) {
		t.Errorf("the last line is not the result envelope: %s", lines[2])
	}
}
