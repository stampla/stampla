package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		a    archive
		want int
	}{
		{
			name: "nothing to do",
			a:    archive{plan: fakePlan(engine.VerifySelf, converged(archiveFile))},
			want: finding.ExitConverged,
		},
		{
			name: "work that needs doing",
			a: archive{plan: fakePlan(engine.Copy,
				converged(archiveFile), incoming(cardFile, filepath.Join(testDest, "b.nef")))},
			want: finding.ExitPending,
		},
		{
			name: "damage dominates work",
			a: archive{plan: fakePlan(engine.VerifySelf,
				incoming(cardFile, filepath.Join(testDest, "b.nef")), corrupt(damagedFile))},
			want: finding.ExitAlarm,
		},
		{
			// The applied half of a run is not pending any more, which is
			// why an import that worked exits 0 and its own preview exits 1.
			name: "work that landed",
			a: archive{
				plan:   fakePlan(engine.Copy, incoming(cardFile, filepath.Join(testDest, "b.nef"))),
				result: &engine.Result{Landed: []engine.Action{{Old: cardFile, New: filepath.Join(testDest, "b.nef")}}, Members: 1},
			},
			want: finding.ExitConverged,
		},
		{
			name: "work that did not land",
			a: archive{
				plan:   fakePlan(engine.Copy, incoming(cardFile, filepath.Join(testDest, "b.nef"))),
				result: &engine.Result{},
			},
			want: finding.ExitPending,
		},
		{
			// There is no finding class for "this could not be written",
			// so nothing in the findings speaks for a failed group.
			name: "a group that would not land",
			a: archive{
				plan: fakePlan(engine.Copy, incoming(cardFile, filepath.Join(testDest, "b.nef"))),
				result: &engine.Result{Failed: []engine.Failure{
					{Key: "b", Path: cardFile, Err: errors.New("read-only file system")},
				}},
			},
			want: finding.ExitAlarm,
		},
		{
			name: "a failure over an otherwise clean run",
			a: archive{
				plan: fakePlan(engine.Move, converged(archiveFile)),
				result: &engine.Result{Failed: []engine.Failure{
					{Key: "a", Path: archiveFile, Err: errors.New("no space left on device")},
				}},
			},
			want: finding.ExitAlarm,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.a); got != tc.want {
				t.Errorf("exitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWorse(t *testing.T) {
	tests := [][3]int{
		{finding.ExitConverged, finding.ExitConverged, finding.ExitConverged},
		{finding.ExitConverged, finding.ExitPending, finding.ExitPending},
		{finding.ExitPending, finding.ExitConverged, finding.ExitPending},
		{finding.ExitPending, finding.ExitAlarm, finding.ExitAlarm},
		{finding.ExitAlarm, finding.ExitPending, finding.ExitAlarm},
		{finding.ExitAlarm, finding.ExitConverged, finding.ExitAlarm},
	}
	for _, tc := range tests {
		if got := worse(tc[0], tc[1]); got != tc[2] {
			t.Errorf("worse(%d, %d) = %d, want %d", tc[0], tc[1], got, tc[2])
		}
	}
}

func TestSectionsOrderAndContent(t *testing.T) {
	a := archive{
		mode: engine.Move,
		plan: fakePlan(engine.Move,
			converged(archiveFile),
			incoming(cardFile, relocatedFile),
			corrupt(damagedFile),
			finding.Finding{Class: finding.Unresolvable, Path: unresolvableFile, Old: unresolvableFile, Detail: "no capture time"},
		),
		result: &engine.Result{
			Landed:  []engine.Action{{Class: finding.Incoming, Old: cardFile, New: relocatedFile}},
			Members: 1,
		},
	}

	found := sections(a)
	var titles []string
	for _, sec := range found {
		titles = append(titles, sec.title)
	}
	// What happened, then damage, then what still needs a person.
	want := []string{"moved", string(finding.Corrupt), string(finding.Unresolvable)}
	if strings.Join(titles, ",") != strings.Join(want, ",") {
		t.Fatalf("sections() = %v, want %v", titles, want)
	}
	if found[0].entries[0].to != relocatedFile {
		t.Errorf("the landed section does not carry where the file went: %+v", found[0].entries[0])
	}
	if !found[1].alarm || !found[1].evidence {
		t.Errorf("damage is not marked as an alarm carrying its evidence: %+v", found[1])
	}
	// Converged files are counted, never listed.
	for _, sec := range found {
		for _, line := range sec.entries {
			if line.from == archiveFile {
				t.Error("a converged file was listed")
			}
		}
	}
}

// TestSectionsPreviewShowsThePlan proves a run that applied nothing
// reports the plan itself, which is the whole point of -n.
func TestSectionsPreviewShowsThePlan(t *testing.T) {
	a := archive{
		mode: engine.Copy,
		plan: fakePlan(engine.Copy, incoming(filepath.Join(testCard, "preview.nef"), filepath.Join(testDest, "2026", "preview.nef"))),
	}
	found := sections(a)
	if len(found) != 1 || found[0].title != string(finding.Incoming) {
		t.Fatalf("sections() = %+v, want the planned work", found)
	}
}

// TestSectionsHideWhatFailed proves a rename that did not happen is
// never printed as one that did.
func TestSectionsHideWhatFailed(t *testing.T) {
	plan := fakePlan(engine.Move, incoming(cardFile, relocatedFile))
	plan.Groups = []engine.GroupPlan{{
		Key:     "b",
		Actions: []engine.Action{{Class: finding.Incoming, Verb: engine.VerbMove, Old: cardFile, New: relocatedFile}},
	}}
	a := archive{
		mode: engine.Move,
		plan: plan,
		result: &engine.Result{Failed: []engine.Failure{
			{Key: "b", Path: cardFile, Err: errors.New("input/output error")},
		}},
	}
	for _, sec := range sections(a) {
		for _, line := range sec.entries {
			if line.from == cardFile {
				t.Errorf("a file whose group failed was reported as %s work", sec.title)
			}
		}
	}
	// It is still wrong, and the exit code still says so.
	if got := exitCode(a); got != finding.ExitAlarm {
		t.Errorf("exitCode() = %d, want the alarm code", got)
	}
}

func TestProvenanceText(t *testing.T) {
	tests := []struct {
		res  layout.Resolution
		want string
	}{
		{res: layout.Resolution{Source: layout.SourceFlag}, want: "from --layout"},
		{res: layout.Resolution{Source: layout.SourceDefault}, want: "from the built-in default"},
		{
			res:  layout.Resolution{Source: layout.SourceConfig, SourcePath: "/home/jkb/.config/stampla/config"},
			want: "from the global config /home/jkb/.config/stampla/config",
		},
		{res: layout.Resolution{Source: testMarker}, want: "from " + testMarker},
	}
	for _, tc := range tests {
		if got := provenanceText(tc.res); got != tc.want {
			t.Errorf("provenanceText(%q) = %q, want %q", tc.res.Source, got, tc.want)
		}
	}
}

func TestHumanReport(t *testing.T) {
	var stdout, stderr strings.Builder
	h := newHuman(&out{w: &stdout}, &out{w: &stderr}, palette{}, false)

	res := declaredIn(testDest, pattern)
	a := archive{
		root: testDest,
		mode: engine.Copy,
		res:  res,
		plan: fakePlan(engine.Copy,
			converged(archiveFile),
			corrupt(damagedFile),
		),
		result:  &engine.Result{Members: 1, Receipt: testReceipt, Landed: []engine.Action{{Old: cardFile, New: relocatedFile}}},
		skipped: scanner.Skipped{Hidden: 3, Other: 1},
		notes:   []string{"hint: a hint", `warning: ` + testMarker + `:3: unknown key "colour"`},
	}
	a.result.Marker = engine.MarkerRecord{Written: true, Path: testMarker, Pattern: pattern}

	h.head(engine.Copy, testDest, false, res)
	h.body(a)
	h.tail(outcome{exit: finding.ExitAlarm})
	report := stdout.String()

	// The provenance line comes first, before any finding: a report that
	// does not say what governed it is a report about nothing in
	// particular.
	if !strings.HasPrefix(report, "layout: "+pattern+" (from "+testMarker+")\n") {
		t.Errorf("the report does not lead with its provenance:\n%s", report)
	}
	for _, want := range []string{
		"copied (1):",
		cardFile + " -> " + relocatedFile,
		"corrupt (1):",
		"the hash disagrees",
		"2 groups, 2 examined: 1 corrupt, 1 converged",
		"applied 1 file, recorded in " + testReceipt,
		`declared layout = "` + pattern + `" in ` + testMarker,
		"skipped 3 hidden, 1 in formats stampla does not name",
		"hint: a hint",
		`warning: ` + testMarker + `:3: unknown key "colour"`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not carry %q:\n%s", want, report)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("the report wrote to stderr: %q", stderr.String())
	}
}

func TestHumanReportColorsDamage(t *testing.T) {
	var stdout, stderr strings.Builder
	h := newHuman(&out{w: &stdout}, &out{w: &stderr}, palette{on: true}, false)
	a := archive{plan: fakePlan(engine.VerifySelf, corrupt(filepath.Join(testDest, "damaged.nef")))}
	h.body(a)

	report := stdout.String()
	if !strings.Contains(report, ansiRed+"corrupt (1):"+ansiReset) {
		t.Errorf("damage is not in red:\n%q", report)
	}
	if strings.Contains(report, ansiRed+"1 groups") {
		t.Errorf("the summary is in red:\n%q", report)
	}
}

func TestFailuresAreReported(t *testing.T) {
	var stdout, stderr strings.Builder
	h := newHuman(&out{w: &stdout}, &out{w: &stderr}, palette{}, false)
	a := archive{
		plan: fakePlan(engine.Move),
		result: &engine.Result{Failed: []engine.Failure{
			{Key: "b", Path: cardFile, Err: errors.New("input/output error"), Reverted: false},
		}},
	}
	h.body(a)

	report := stdout.String()
	for _, want := range []string{"failed (1):", cardFile, "input/output error", "part-applied"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not carry %q:\n%s", want, report)
		}
	}
}

func TestCount(t *testing.T) {
	if got := count(1, "file"); got != "1 file" {
		t.Errorf("count(1) = %q", got)
	}
	if got := count(2, "file"); got != "2 files" {
		t.Errorf("count(2) = %q", got)
	}
	if got := count(0, "group"); got != "0 groups" {
		t.Errorf("count(0) = %q", got)
	}
}

func TestSkippedLine(t *testing.T) {
	if got := skippedLine(scanner.Skipped{}); got != "" {
		t.Errorf("skippedLine(nothing) = %q, want silence", got)
	}
	if got := skippedLine(scanner.Skipped{Hidden: 2}); got != "skipped 2 hidden" {
		t.Errorf("skippedLine(hidden) = %q", got)
	}
}

// The files the fabricated findings are about, spelled the way this
// platform spells them.
var (
	archiveFile      = filepath.Join(testDest, "a.jpg")
	cardFile         = filepath.Join(testCard, "b.nef")
	relocatedFile    = filepath.Join(testDest, "2026", "b.nef")
	damagedFile      = filepath.Join(testDest, "c.nef")
	unresolvableFile = filepath.Join(testDest, "d.mp4")
	testReceipt      = filepath.Join(testDest, engine.ReceiptName)
)

// Findings a report is built from.

func converged(path string) finding.Finding {
	return finding.Finding{Class: finding.Converged, Path: path, Old: path, Detail: "name, hash and location all match"}
}

func incoming(from, to string) finding.Finding {
	return finding.Finding{Class: finding.Incoming, Path: from, Old: from, New: to, Detail: "named from EXIF:DateTimeOriginal"}
}

func corrupt(path string) finding.Finding {
	return finding.Finding{Class: finding.Corrupt, Path: path, Old: path, Detail: "the hash disagrees with the name"}
}
