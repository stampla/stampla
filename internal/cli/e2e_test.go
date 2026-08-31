package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// These tests drive the real interface over real files: the flags, the
// guardrails, the engine, the report and the exit code, with nothing
// stubbed but the terminal a confirmation would be asked on. What they
// assert is what a person at a shell would see.

// TestCopyIntoEmptyArchive is the first thing anybody does with this
// tool: a card, an empty directory, and afterwards an archive.
func TestCopyIntoEmptyArchive(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.StampVideo(t, filepath.Join(card, "DSC_9999.MP4"), testutil.VideoDate)

	got := runCLI(t, nil, "cp", card, dest)
	if got.code != 0 {
		t.Fatalf("cp = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}

	want := []string{
		layout.MarkerName,
		engine.ReceiptName,
		path.Join(dateDir, jpegName),
		path.Join(dateDir, videoName),
	}
	slices.Sort(want)
	if found := testutil.RelPaths(t, dest); !slices.Equal(found, want) {
		t.Errorf("the archive holds\n %q\nwant\n %q", found, want)
	}
	// The sources are still there: cp never removes anything.
	if found := testutil.RelPaths(t, card); len(found) != 2 {
		t.Errorf("cp changed the card: %q", found)
	}

	marker := readFile(t, filepath.Join(dest, layout.MarkerName))
	if !strings.Contains(marker, `layout = "`+pattern+`"`) {
		t.Errorf("the marker does not record the layout that shaped the tree: %q", marker)
	}
	receipt := readFile(t, filepath.Join(dest, engine.ReceiptName))
	if strings.Count(receipt, "\n") != 2 || !strings.Contains(receipt, "\tcp\t") {
		t.Errorf("the receipt does not record both copies: %q", receipt)
	}
	if !strings.Contains(got.stdout, "applied 2 files") {
		t.Errorf("the report does not say what it did:\n%s", got.stdout)
	}
}

// TestCardGuard is the question a memory card is formatted on, and the
// answer changing the moment a file leaves the archive.
func TestCardGuard(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.StampVideo(t, filepath.Join(card, "DSC_9999.MP4"), testutil.VideoDate)

	if got := runCLI(t, nil, "cp", card, dest); got.code != 0 {
		t.Fatalf("cp = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	if got := runCLI(t, nil, "verify", card, dest); got.code != 0 {
		t.Fatalf("verify card dest = %d, want 0 — the card is not accounted for\n%s", got.code, got.stdout)
	}

	// One file leaves the archive, and the card stops being safe to
	// format.
	if err := os.Remove(filepath.Join(dest, dateDir, videoName)); err != nil {
		t.Fatalf("removing a file from the archive: %v", err)
	}
	got := runCLI(t, nil, "verify", card, dest)
	if got.code != 1 {
		t.Fatalf("verify card dest = %d, want 1\n%s", got.code, got.stdout)
	}
	if !strings.Contains(got.stdout, "missing (1):") ||
		!strings.Contains(got.stdout, "DSC_9999.MP4") {
		t.Errorf("the report does not name what is missing:\n%s", got.stdout)
	}
}

// TestMoveInPlaceUndeclared proves the placement rule: a fallback layout
// may name files and may never reorganize an archive nobody declared.
func TestMoveInPlaceUndeclared(t *testing.T) {
	testutil.RequireExifTool(t)
	dest := t.TempDir()
	testutil.StampJPEG(t, filepath.Join(dest, "holiday", "DSC_1234.JPG"), testutil.JPEGDate)

	got := runCLI(t, nil, "mv", dest, dest)
	if got.code != 0 {
		t.Fatalf("mv = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	want := []string{engine.ReceiptName, path.Join("holiday", jpegName)}
	if found := testutil.RelPaths(t, dest); !slices.Equal(found, want) {
		t.Errorf("the archive holds\n %q\nwant\n %q — the name converged, the directory did not", found, want)
	}
	// No marker: recording the fallback would tell the next run to
	// reorganize a tree nobody asked it to touch.
	if _, err := os.Stat(filepath.Join(dest, layout.MarkerName)); !os.IsNotExist(err) {
		t.Errorf("an undeclared run wrote a marker: %v", err)
	}
	if !strings.Contains(got.stdout, "declares no layout of its own") {
		t.Errorf("the report does not say why nothing moved:\n%s", got.stdout)
	}
}

// TestStdinNulSeparated feeds the interface a file list the way
// find -print0 does.
func TestStdinNulSeparated(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	wanted := filepath.Join(card, "DSC_1234.JPG")
	testutil.StampJPEG(t, wanted, testutil.JPEGDate)
	testutil.StampVideo(t, filepath.Join(card, "DSC_9999.MP4"), testutil.VideoDate)

	list := bytes.NewBufferString(wanted + "\x00")
	got := runCLI(t, list, "cp", "--stdin", "-z", dest)
	if got.code != 0 {
		t.Fatalf("cp --stdin -z = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	want := []string{layout.MarkerName, engine.ReceiptName, path.Join(dateDir, jpegName)}
	slices.Sort(want)
	if found := testutil.RelPaths(t, dest); !slices.Equal(found, want) {
		t.Errorf("the archive holds\n %q\nwant\n %q — only the listed file", found, want)
	}
}

// TestDAMMarkerRefusesTheRun proves the refusal that no answer to a
// prompt can get past.
func TestDAMMarkerRefusesTheRun(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName: "layout = \"" + pattern + "\"\ndam = \"lrc\"\n",
	})

	got := runCLI(t, nil, "cp", card, dest)
	if got.code != 2 {
		t.Fatalf("cp into a dam archive = %d, want 2\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "--inject") || !strings.Contains(got.stderr, `dam = "lrc"`) {
		t.Errorf("the refusal does not name the way forward:\n%s", got.stderr)
	}
	if found := testutil.RelPaths(t, dest); !slices.Equal(found, []string{layout.MarkerName}) {
		t.Errorf("the refused run wrote something: %q", found)
	}
}

// TestConfirmationRefusedWithoutTerminal proves an unattended run that
// trips a tripwire stops, and stops before anything is written.
func TestConfirmationRefusedWithoutTerminal(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.Tree(t, dest, map[string]string{"Lightroom.lrcat": "a catalog"})
	before := snapshot(t, dest)

	got := runCLI(t, nil, "mv", card, dest)
	if got.code != 3 {
		t.Fatalf("mv beside a catalog = %d, want 3\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "-y") || !strings.Contains(got.stderr, "Lightroom.lrcat") {
		t.Errorf("the refusal does not say why or how to proceed:\n%s", got.stderr)
	}
	if after := snapshot(t, dest); !equalTrees(before, after) {
		t.Errorf("a refused run wrote to the archive:\n%v\n%v", before, after)
	}
	if found := testutil.RelPaths(t, card); len(found) != 1 {
		t.Errorf("a refused mv touched the source: %q", found)
	}
}

// TestConfirmationAnswered proves yes is an answer too, and that the
// question came from the terminal rather than from stdin.
func TestConfirmationAnswered(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.Tree(t, dest, map[string]string{"Lightroom.lrcat": "a catalog"})

	got := runCLIAnswering(t, "y\n", nil, "mv", card, dest)
	if got.code != 0 {
		t.Fatalf("mv answered yes = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "proceed? [y/N]") {
		t.Errorf("no question was asked:\n%s", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dest, dateDir, jpegName)); err != nil {
		t.Errorf("the file did not land: %v", err)
	}
	if found := testutil.RelPaths(t, card); len(found) != 0 {
		t.Errorf("mv left the source behind: %q", found)
	}
}

// TestDryRunWritesNothing proves the preview is the absence of Apply,
// byte for byte on both sides.
func TestDryRunWritesNothing(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	beforeCard, beforeDest := snapshot(t, card), snapshot(t, dest)

	got := runCLI(t, nil, "cp", "-n", card, dest)
	// A preview of pending work reports pending work: the findings are
	// all still true afterwards.
	if got.code != 1 {
		t.Fatalf("cp -n = %d, want 1\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "dry run: nothing was written (1 file would be)") {
		t.Errorf("the preview does not say it wrote nothing:\n%s", got.stdout)
	}
	if !equalTrees(beforeCard, snapshot(t, card)) || !equalTrees(beforeDest, snapshot(t, dest)) {
		t.Error("a dry run changed a file")
	}

	// And the same command without -n does what it previewed.
	if applied := runCLI(t, nil, "cp", card, dest); applied.code != 0 {
		t.Fatalf("cp = %d, want 0\n%s%s", applied.code, applied.stdout, applied.stderr)
	}
	if _, err := os.Stat(filepath.Join(dest, dateDir, jpegName)); err != nil {
		t.Errorf("the previewed file did not land: %v", err)
	}
}

// TestPorcelainStream reads the machine interface the way the desktop
// app will: parse every line, refuse a format it does not know, and take
// the exit code from the last one.
func TestPorcelainStream(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)

	got := runCLI(t, nil, "cp", "--porcelain", card, dest)
	if got.code != 0 {
		t.Fatalf("cp --porcelain = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}

	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("the stream is %d lines:\n%s", len(lines), got.stdout)
	}
	var events []map[string]any
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("unparsable line %q: %v", line, err)
		}
		events = append(events, event)
	}

	first, last := events[0], events[len(events)-1]
	if first["type"] != "plan" || first["format"] != float64(porcelainFormat) {
		t.Errorf("the stream does not open with format 1: %v", first)
	}
	if first["mode"] != "cp" || first["dest"] != dest {
		t.Errorf("the plan envelope does not say what run this was: %v", first)
	}
	if last["type"] != "result" || last["exit"] != float64(0) || last["applied"] != float64(1) {
		t.Errorf("the stream does not close with its result: %v", last)
	}
	for _, event := range events[1 : len(events)-1] {
		switch event["type"] {
		case "finding", "progress":
		default:
			t.Errorf("unexpected event in the stream: %v", event)
		}
	}
	// Progress belongs in the stream, never on the report's stream.
	if strings.Contains(got.stderr, "\r") {
		t.Errorf("--porcelain drew a progress line: %q", got.stderr)
	}
}

// TestVerifyDescendsNestedRoots proves each archive is judged under its
// own declaration: the nested root's flat layout is not the parent's
// dated one, and neither archive is reported against the other's.
func TestVerifyDescendsNestedRoots(t *testing.T) {
	testutil.RequireExifTool(t)
	dest := t.TempDir()
	nested := filepath.Join(dest, "scans")
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:            "layout = \"" + pattern + "\"\n",
		"scans/" + layout.MarkerName: "layout = \"\"\n",
	})
	testutil.StampJPEG(t, filepath.Join(dest, dateDir, jpegName), testutil.JPEGDate)
	testutil.StampVideo(t, filepath.Join(nested, videoName), testutil.VideoDate)

	got := runCLI(t, nil, "verify", dest)
	if got.code != 0 {
		t.Fatalf("verify = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	if strings.Count(got.stdout, "layout: ") != 2 {
		t.Errorf("the report does not state a provenance per archive:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "archive "+nested+" (nested)") {
		t.Errorf("the nested archive is not named:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, `layout: "" (flat) (from `+filepath.Join(nested, layout.MarkerName)) {
		t.Errorf("the nested archive was not judged by its own declaration:\n%s", got.stdout)
	}
}

// TestVerifyOfADamagedName is the alarm: a write-once file whose content
// no longer matches its name is never renamed, and the run says so at
// the alarm exit code.
func TestVerifyOfADamagedName(t *testing.T) {
	testutil.RequireExifTool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{layout.MarkerName: "layout = \"" + pattern + "\"\n"})
	// A video named with somebody else's hash: the name claims an
	// identity the payload does not support.
	damaged := "20260703_130727_deadbeef.mp4"
	testutil.StampVideo(t, filepath.Join(dest, dateDir, damaged), testutil.VideoDate)

	got := runCLI(t, nil, "verify", dest)
	if got.code != 2 {
		t.Fatalf("verify = %d, want 2\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "corrupt (1):") || !strings.Contains(got.stdout, damaged) {
		t.Errorf("the report does not name the damage:\n%s", got.stdout)
	}
	if found := testutil.RelPaths(t, dest); !slices.Contains(found, path.Join(dateDir, damaged)) {
		t.Errorf("verify renamed a damaged file: %q", found)
	}
}

// TestColorAlwaysReachesAPipe proves --color is an answer rather than a
// preference: a person piping into something that renders color gets it.
func TestColorAlwaysReachesAPipe(t *testing.T) {
	testutil.RequireExifTool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{layout.MarkerName: "layout = \"" + pattern + "\"\n"})
	testutil.StampVideo(t, filepath.Join(dest, dateDir, "20260703_130727_deadbeef.mp4"), testutil.VideoDate)

	got := runCLI(t, nil, "verify", "--color=always", dest)
	if !strings.Contains(got.stdout, ansiRed+"corrupt (1):"+ansiReset) {
		t.Errorf("--color=always drew no color:\n%q", got.stdout)
	}
	plain := runCLI(t, nil, "verify", "--color=never", dest)
	if strings.Contains(plain.stdout, "\x1b[") {
		t.Errorf("--color=never drew color:\n%q", plain.stdout)
	}
	// auto in a pipe is never, which is what a test's buffer is.
	auto := runCLI(t, nil, "verify", dest)
	if strings.Contains(auto.stdout, "\x1b[") {
		t.Errorf("auto drew color into a pipe:\n%q", auto.stdout)
	}
}

// TestDestinationInsideAnArchive is the guardrail cp and mv have and
// verify does not.
func TestDestinationInsideAnArchive(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.Tree(t, dest, map[string]string{layout.MarkerName: "layout = \"" + pattern + "\"\n"})
	inside := filepath.Join(dest, "2026")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("creating %s: %v", inside, err)
	}

	got := runCLI(t, nil, "cp", card, inside)
	if got.code != 2 {
		t.Fatalf("cp into a subdirectory of an archive = %d, want 2\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "inside an archive rooted at "+dest) {
		t.Errorf("the refusal does not name the archive to use:\n%s", got.stderr)
	}
	// verify answers the same question happily: it writes nothing.
	if verify := runCLI(t, nil, "verify", inside); verify.code != 0 {
		t.Errorf("verify of a subtree = %d, want 0\n%s%s", verify.code, verify.stdout, verify.stderr)
	}
}

// TestContainerRefused proves a directory that holds archives is not one.
func TestContainerRefused(t *testing.T) {
	testutil.RequireExifTool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName: "layout-for-children = \"" + pattern + "\"\n",
	})

	got := runCLI(t, nil, "cp", card, dest)
	if got.code != 2 {
		t.Fatalf("cp into a container = %d, want 2\n%s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "archives beneath it") {
		t.Errorf("the refusal does not say what to do instead:\n%s", got.stderr)
	}
}

// TestLayoutFlagPlacesFiles covers --layout, including the empty pattern
// that means flat.
func TestLayoutFlagPlacesFiles(t *testing.T) {
	testutil.RequireExifTool(t)
	card := t.TempDir()
	testutil.StampJPEG(t, filepath.Join(card, "DSC_1234.JPG"), testutil.JPEGDate)

	dated := t.TempDir()
	if got := runCLI(t, nil, "cp", "--layout", "{yyyy-mm-dd}", card, dated); got.code != 0 {
		t.Fatalf("cp --layout = %d\n%s%s", got.code, got.stdout, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dated, "2026-07-03", jpegName)); err != nil {
		t.Errorf("--layout did not place the file: %v", err)
	}

	flat := t.TempDir()
	got := runCLI(t, nil, "cp", "--layout", "", card, flat)
	if got.code != 0 {
		t.Fatalf(`cp --layout "" = %d\n%s%s`, got.code, got.stdout, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(flat, jpegName)); err != nil {
		t.Errorf("the flat layout did not place the file at the root: %v", err)
	}
	if !strings.Contains(got.stdout, `layout: "" (flat) (from --layout)`) {
		t.Errorf("the report does not state the flat layout and where it came from:\n%s", got.stdout)
	}
}

// Tree snapshots, for the runs that must change nothing.

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	found := make(map[string]string)
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		found[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found
}

func equalTrees(before, after map[string]string) bool {
	if len(before) != len(after) {
		return false
	}
	for name, sum := range before {
		if after[name] != sum {
			return false
		}
	}
	return true
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}
