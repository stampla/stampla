package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
)

// The canonical names the committed fixtures produce, stated rather than
// recomputed: a test that derived the expected name the way the code
// does would pass however wrong both were.
const (
	jpegName  = "20260703_150727_0a8c8109.jpg"
	videoName = "20260703_130727_082746c9.mp4"
	dateDir   = "2026/2026-07"
	pattern   = "{yyyy}/{yyyy}-{mm}"
)

// The paths the fabricated plans are about. Nothing reads them from
// disk; they are paths for a report and a stream to quote.
//
// They are built with filepath, not written out, because the code under
// test joins and renders paths the way this platform spells them — a
// marker's path is filepath.Join, so it wears backslashes on Windows and
// a drive letter in front. An expectation written as "/photos/.stampla"
// would be an expectation that only ever held on Unix, and the fix for
// that must not be a weaker assertion: every expectation below is built
// from these same values.
var (
	testDest   = testPath("photos")
	testCard   = testPath("card")
	testMarker = filepath.Join(testDest, layout.MarkerName)
)

// testPath is an absolute path on this platform: rooted at the volume
// the tests are running on, so Windows gets a drive letter rather than a
// path no filesystem there would accept.
func testPath(elem ...string) string {
	root := string(filepath.Separator)
	if wd, err := os.Getwd(); err == nil {
		root = filepath.VolumeName(wd) + root
	}
	return filepath.Join(append([]string{root}, elem...)...)
}

// TestFixturePathsAreThisPlatformsPaths pins the derivation every
// expectation in this package rests on.
//
// A marker's path is whatever the code's own filepath.Join makes of it,
// so a fixture written out as "/photos/.stampla" is a fixture that
// agrees with the code on Unix and disagrees with it on Windows, where
// the separator and the volume are both different. Building the fixtures
// with filepath is what keeps the assertions exact rather than lenient.
func TestFixturePathsAreThisPlatformsPaths(t *testing.T) {
	if !filepath.IsAbs(testDest) {
		t.Errorf("testDest = %q, which is not an absolute path on this platform", testDest)
	}
	if rendered := (&layout.Marker{Dir: testDest}).Path(); rendered != testMarker {
		t.Errorf("testMarker = %q, but the code renders it %q", testMarker, rendered)
	}
	if strings.ContainsRune(testMarker, '/') != (filepath.Separator == '/') {
		t.Errorf("testMarker = %q, which is not spelled with this platform's separator %q",
			testMarker, filepath.Separator)
	}
}

// runResult is what one drive of the interface produced.
type runResult struct {
	code   int
	stdout string
	stderr string
}

// runCLI drives the interface exactly as main does, with streams a test
// can read and no terminal to prompt on.
//
// The terminal is injected rather than looked up, because whether
// /dev/tty happens to open depends on how the test binary was started —
// and a test that hangs waiting for an answer nobody is there to give is
// the one failure mode a confirmation must never have.
func runCLI(t *testing.T, stdin io.Reader, args ...string) runResult {
	t.Helper()
	return runCLIAnswering(t, "", stdin, args...)
}

// runCLIAnswering is runCLI with somebody at the terminal, typing
// answers. An empty answers string means there is no terminal at all.
func runCLIAnswering(t *testing.T, answers string, stdin io.Reader, args ...string) runResult {
	t.Helper()
	var stdout, stderr strings.Builder
	e := &env{
		version: "test",
		stdin:   stdin,
		out:     &out{w: &stdout},
		errOut:  &out{w: &stderr},
		terminal: func() (io.ReadCloser, error) {
			if answers == "" {
				return nil, errors.New("no terminal")
			}
			return io.NopCloser(strings.NewReader(answers)), nil
		},
	}
	defer e.closeTerminal()
	code := e.dispatch(args)
	return runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// harness is an env whose prompts can be read back.
type harness struct {
	*env
	stderr *strings.Builder
}

func (h *harness) prompted() string { return h.stderr.String() }

// envWithTerminal is an env with somebody at the terminal typing
// answers. An empty string is a terminal that answers nothing, which is
// end of input rather than no terminal.
func envWithTerminal(answers string) *harness {
	h := blankEnv()
	h.terminal = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(answers)), nil
	}
	return h
}

// envWithoutTerminal is an env with no terminal to ask on: cron, a
// service, a pipeline.
func envWithoutTerminal() *harness {
	h := blankEnv()
	h.terminal = func() (io.ReadCloser, error) { return nil, errors.New("no terminal") }
	return h
}

func blankEnv() *harness {
	var stdout, stderr strings.Builder
	return &harness{
		env:    &env{version: "test", out: &out{w: &stdout}, errOut: &out{w: &stderr}},
		stderr: &stderr,
	}
}

// fakePlan builds a plan out of nothing, for the predicates and the
// report renderers that read one. Nothing here touches a filesystem: a
// tripwire is a function of a plan, and it is tested as one.
func fakePlan(mode engine.Mode, findings ...finding.Finding) *engine.Plan {
	plan := &engine.Plan{
		Mode:       mode,
		Dest:       testDest,
		Resolution: layout.Resolution{Pattern: layout.MustParsePattern(pattern), Source: layout.SourceDefault},
		Findings:   findings,
		Counts:     make(map[finding.Class]int),
	}
	for _, f := range findings {
		plan.Counts[f.Class]++
		// One file to a group, which is what most groups are.
		plan.Groups = append(plan.Groups, engine.GroupPlan{
			Key:     f.Path,
			Class:   f.Class,
			Actions: []engine.Action{{Class: f.Class, Old: f.Old, New: f.New, Detail: f.Detail}},
		})
	}
	return plan
}

// declaredIn is a resolution that came from the destination's own
// marker, which is the only kind that may reorganize an archive.
func declaredIn(dir, declared string) layout.Resolution {
	marker := &layout.Marker{Dir: dir, Layout: declared}
	return layout.Resolution{
		Pattern:    layout.MustParsePattern(declared),
		Source:     marker.Path(),
		SourcePath: marker.Path(),
		Declared:   true,
		Marker:     marker,
	}
}
