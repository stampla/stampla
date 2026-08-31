package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/exif"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
	"github.com/stampla/stampla/internal/testutil"
)

// The canonical names the committed fixtures produce. Stating them
// rather than recomputing them is the point: a test that derived the
// expected name the same way the code does would pass however wrong
// both were.
const (
	jpegName  = "20260703_150727_0a8c8109.jpg"
	videoName = "20260703_130727_082746c9.mp4"
	movName   = "20260703_130727_082746c9.mov"
	dateDir   = "2026/2026-07"
	pattern   = "{yyyy}/{yyyy}-{mm}"
)

// newPool starts an ExifTool pool for one test, skipping when ExifTool
// is not installed.
func newPool(t *testing.T) *exif.Pool {
	t.Helper()
	testutil.RequireExifTool(t)
	pool, err := exif.NewPool(2)
	if err != nil {
		t.Fatalf("starting an exiftool pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// scanOf collects inputs the way a mutation verb does.
func scanOf(t *testing.T, inputs ...string) *scanner.Scan {
	t.Helper()
	scan, err := scanner.Collect(inputs, scanner.Options{StopAtRoots: true})
	if err != nil {
		t.Fatalf("collecting %v: %v", inputs, err)
	}
	return scan
}

// fallbackLayout is the built-in default: it may place files entering
// the root, and may never reorganize what is already there.
func fallbackLayout(t *testing.T) layout.Resolution {
	t.Helper()
	return layout.Resolution{
		Pattern: layout.MustParsePattern(pattern),
		Source:  layout.SourceDefault,
	}
}

// declaredLayout is a layout the destination declared for itself, which
// is the only kind that may relocate files already under the root.
func declaredLayout(t *testing.T, dest string) layout.Resolution {
	t.Helper()
	marker, err := layout.ReadMarker(dest)
	if err != nil {
		t.Fatalf("reading the marker of %s: %v", dest, err)
	}
	return layout.Resolution{
		Pattern:    layout.MustParsePattern(pattern),
		Source:     filepath.Join(dest, layout.MarkerName),
		SourcePath: filepath.Join(dest, layout.MarkerName),
		Declared:   true,
		Marker:     marker,
	}
}

// mustPlan builds a plan and fails the test if it cannot.
func mustPlan(t *testing.T, opts Options) *Plan {
	t.Helper()
	plan, err := BuildPlan(opts)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return plan
}

// mustApply applies a plan and fails the test if any group did not land.
func mustApply(t *testing.T, plan *Plan, opts ApplyOptions) *Result {
	t.Helper()
	result, err := Apply(plan, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Failed) > 0 {
		t.Fatalf("Apply reported failures: %v", result.Failed)
	}
	return result
}

// classOf is the class the plan gave one path, and whether it mentioned
// it at all.
func classOf(plan *Plan, path string) (Action, bool) {
	for _, group := range plan.Groups {
		for _, action := range group.Actions {
			if action.Old == path {
				return action, true
			}
		}
	}
	return Action{}, false
}

// wantClass asserts the class of one path, quoting the plan when it
// disagrees.
func wantClass(t *testing.T, plan *Plan, path string, class finding.Class) Action {
	t.Helper()
	action, ok := classOf(plan, path)
	if !ok {
		t.Fatalf("the plan says nothing about %s\n%s", path, dumpPlan(plan))
	}
	if action.Class != class {
		t.Fatalf("%s: class %q, want %q (%s)\n%s",
			path, action.Class, class, action.Detail, dumpPlan(plan))
	}
	return action
}

// dumpPlan renders a plan for a failure message.
func dumpPlan(plan *Plan) string {
	var b strings.Builder
	b.WriteString("plan:\n")
	for _, group := range plan.Groups {
		for _, action := range group.Actions {
			b.WriteString("  " + string(action.Class) + " " + string(action.Verb) +
				" " + action.Old + " -> " + action.New + " (" + action.Detail + ")\n")
		}
	}
	return b.String()
}

// wantTree asserts the exact set of files under a root.
func wantTree(t *testing.T, root string, want ...string) {
	t.Helper()
	got := testutil.RelPaths(t, root)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tree under %s:\n got %v\nwant %v", root, got, want)
	}
}

// receiptLines reads the destination's receipt.
func receiptLines(t *testing.T, dest string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dest, ReceiptName))
	if err != nil {
		t.Fatalf("reading the receipt: %v", err)
	}
	var lines [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line != "" {
			lines = append(lines, strings.Split(line, "\t"))
		}
	}
	return lines
}

// The markers in the fixtures whose payload a test corrupts: the JPEG's
// start-of-scan and the MP4's media data atom. A byte is flipped just
// inside each, which is entropy-coded data in one and a compressed frame
// in the other, so the image-data hash moves and the file stays
// readable.
var payloadMarkers = map[string]struct {
	marker []byte
	skip   int
}{
	".jpg": {marker: []byte{0xFF, 0xDA}, skip: 8},
	".mp4": {marker: []byte("mdat"), skip: 68},
	".mov": {marker: []byte("mdat"), skip: 68},
}

// corruptPayload flips one byte of a file's image or video payload, the
// way a failing disk would, and proves it did: the image-data hash must
// move, or the test that relies on it would be testing nothing.
func corruptPayload(t *testing.T, path string) {
	t.Helper()
	spec, ok := payloadMarkers[strings.ToLower(filepath.Ext(path))]
	if !ok {
		t.Fatalf("no payload marker known for %s", path)
	}
	before := testutil.ImageDataHash(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	index := bytes.Index(data, spec.marker)
	if index < 0 || index+spec.skip >= len(data) {
		t.Fatalf("%s: no %q payload marker to corrupt", path, spec.marker)
	}
	data[index+spec.skip] ^= 0xFF
	testutil.WriteFile(t, path, data)
	if after := testutil.ImageDataHash(t, path); after == before {
		t.Fatalf("%s: flipping a payload byte did not move the image-data hash (%s)",
			path, before)
	}
}
