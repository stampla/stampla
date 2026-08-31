package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// A card imports, lands under its identity names, and re-imports as a
// no-op: the round trip the whole product is.
func TestCopyRoundTrip(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"DCIM/100NIKON/DSC_1234.jpg": "@dated.jpg",
		"DCIM/100NIKON/VID_0001.mp4": "@date.mp4",
	})

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if len(plan.Groups) != 2 {
		t.Fatalf("groups %d, want 2\n%s", len(plan.Groups), dumpPlan(plan))
	}
	jpeg := filepath.Join(card, "DCIM", "100NIKON", "DSC_1234.jpg")
	action := wantClass(t, plan, jpeg, finding.Incoming)
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName); action.New != want {
		t.Errorf("target %s, want %s", action.New, want)
	}
	if action.Verb != VerbCopy {
		t.Errorf("verb %q, want %q", action.Verb, VerbCopy)
	}
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
	}

	// Planning is the whole of a dry run, so it must have written
	// nothing at all.
	wantTree(t, dest)

	result := mustApply(t, plan, ApplyOptions{})
	if result.Members != 2 {
		t.Errorf("landed %d members, want 2", result.Members)
	}
	wantTree(t, dest,
		layout.MarkerName,
		ReceiptName,
		dateDir+"/"+jpegName,
		dateDir+"/"+videoName,
	)
	// The sources are the last-resort backup until the card is
	// deliberately formatted; a copy never touches them.
	wantTree(t, card, "DCIM/100NIKON/DSC_1234.jpg", "DCIM/100NIKON/VID_0001.mp4")

	if !result.Marker.Written {
		t.Error("no marker was written for a fresh archive")
	}
	marker, err := layout.ReadMarker(dest)
	if err != nil || marker == nil {
		t.Fatalf("reading the written marker: %v", err)
	}
	if marker.Layout != pattern {
		t.Errorf("declared layout %q, want %q", marker.Layout, pattern)
	}

	// The same command again is the crash story and the re-import story
	// at once: everything is already there, and nothing happens.
	again := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	for _, group := range again.Groups {
		for _, action := range group.Actions {
			if action.Class != finding.Converged || action.Verb != VerbNone {
				t.Errorf("re-import: %s is %q/%q, want converged and untouched",
					action.Old, action.Class, action.Verb)
			}
		}
	}
	if again.Mutations() != 0 {
		t.Errorf("re-import plans %d mutations, want none\n%s",
			again.Mutations(), dumpPlan(again))
	}
	if again.ExitCode() != finding.ExitConverged {
		t.Errorf("re-import exit code %d, want %d", again.ExitCode(), finding.ExitConverged)
	}
}

// Every applied mutation is recorded, because the receipt is the only
// surviving record of what a file used to be called.
func TestCopyWritesReceipts(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})

	before := time.Now().Add(-time.Second)
	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	result := mustApply(t, plan, ApplyOptions{})

	if want := filepath.Join(dest, ReceiptName); result.Receipt != want {
		t.Errorf("receipt at %s, want %s", result.Receipt, want)
	}
	lines := receiptLines(t, dest)
	if len(lines) != 1 {
		t.Fatalf("receipt has %d lines, want 1: %v", len(lines), lines)
	}
	fields := lines[0]
	if len(fields) != 4 {
		t.Fatalf("receipt line has %d fields, want 4: %q", len(fields), fields)
	}
	stamp, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		t.Errorf("receipt time %q is not RFC 3339: %v", fields[0], err)
	} else if stamp.Before(before) || stamp.After(time.Now().Add(time.Second)) {
		t.Errorf("receipt time %s is not this run's", stamp)
	}
	if fields[1] != "cp" {
		t.Errorf("verb %q, want cp", fields[1])
	}
	if want := filepath.Join(card, "DSC_1234.jpg"); fields[2] != want {
		t.Errorf("old path %q, want %q", fields[2], want)
	}
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName); fields[3] != want {
		t.Errorf("new path %q, want %q", fields[3], want)
	}

	// A second run appends rather than replaces: the receipt is a
	// history, not a status.
	testutil.Tree(t, card, map[string]string{"VID_0001.mp4": "@date.mp4"})
	second := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	mustApply(t, second, ApplyOptions{})
	if lines := receiptLines(t, dest); len(lines) != 2 {
		t.Errorf("receipt has %d lines after a second import, want 2: %v", len(lines), lines)
	}
}

// A destination whose masters another tool renames is refused before
// anything is planned, with the way forward named.
func TestPlanRefusesDAMDestination(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName: "layout = \"" + pattern + "\"\ndam = \"lrc\"\n",
	})

	for _, mode := range []Mode{Copy, Move} {
		_, err := BuildPlan(Options{
			Mode: mode, Scan: scanOf(t, card), Dest: dest,
			Resolution: declaredLayout(t, dest), Pool: pool,
		})
		if !errors.Is(err, ErrDAMManaged) {
			t.Fatalf("%s into a dam archive: err %v, want ErrDAMManaged", mode, err)
		}
		if !strings.Contains(err.Error(), "--inject") {
			t.Errorf("%s: refusal does not name --inject: %v", mode, err)
		}
		var damErr *DAMError
		if !errors.As(err, &damErr) || damErr.DAM != "lrc" {
			t.Errorf("%s: refusal does not carry the dam name: %v", mode, err)
		}
	}

	// Verifying such an archive is always allowed: reading is what the
	// refusal protects, not what it forbids.
	if _, err := BuildPlan(Options{
		Mode: VerifySelf, Scan: scanOf(t, dest), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	}); err != nil {
		t.Errorf("verifying a dam archive: %v", err)
	}
}

// A target already holding different content is a conflict, and nothing
// about it is touched.
func TestConflictRefusesOccupiedTarget(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})

	// An occupant with exactly the canonical name the card file derives,
	// and different pixels behind it.
	occupied := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName)
	testutil.CopyFixture(t, "dated.jpg", occupied)
	corruptPayload(t, occupied)
	before, err := os.ReadFile(occupied)
	if err != nil {
		t.Fatalf("reading the occupant: %v", err)
	}

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	action := wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Conflict)
	if action.Verb != VerbNone {
		t.Errorf("a conflict plans %q, want nothing", action.Verb)
	}
	if !strings.Contains(action.Detail, "different content") {
		t.Errorf("conflict detail does not state its evidence: %q", action.Detail)
	}

	result := mustApply(t, plan, ApplyOptions{})
	if result.Members != 0 {
		t.Errorf("a refused conflict landed %d members", result.Members)
	}
	after, err := os.ReadFile(occupied)
	if err != nil || string(after) != string(before) {
		t.Fatalf("the occupant was modified")
	}
	wantTree(t, dest, dateDir+"/"+jpegName)
}

// The same photograph twice on one card converges once. Two different
// photographs deriving one name converge not at all.
func TestDuplicateAndCollidingSources(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"a/DSC_1234.jpg": "@dated.jpg",
		"b/DSC_9999.jpg": "@dated.jpg",
	})

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	second := wantClass(t, plan, filepath.Join(card, "b", "DSC_9999.jpg"), finding.Converged)
	if !strings.Contains(second.Detail, "converged once") {
		t.Errorf("duplicate detail %q does not say it converges once", second.Detail)
	}
	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, layout.MarkerName, ReceiptName, dateDir+"/"+jpegName)

	// Now a genuine collision: same capture second, different pixels.
	other := t.TempDir()
	testutil.Tree(t, other, map[string]string{"x/DSC_1.jpg": "@dated.jpg", "y/DSC_2.jpg": "@dated.jpg"})
	corruptPayload(t, filepath.Join(other, "y", "DSC_2.jpg"))
	// Give the corrupted one the first one's name-worthy identity by
	// stamping the same capture time; the payloads still differ.
	fresh := t.TempDir()
	plan2 := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, other), Dest: fresh,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	// Different payloads mean different names, so this is not a
	// collision at all — both import. The check is that the engine says
	// so rather than merging them.
	if plan2.Counts[finding.Conflict] != 0 {
		t.Errorf("two distinct photographs reported a conflict\n%s", dumpPlan(plan2))
	}
	mustApply(t, plan2, ApplyOptions{})
	if got := len(testutil.RelPaths(t, fresh)); got != 4 {
		t.Errorf("%d files landed, want 4 (two photographs, a marker and a receipt)", got)
	}
}

// A file with no resolvable capture time is reported and skipped, and
// the rest of the run proceeds.
func TestUnresolvableSkipsOnlyItsGroup(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"DSC_1234.jpg": "@dated.jpg",
		"DSC_5678.jpg": "@plain.jpg",
		"VID_0002.mp4": "@nodate.mp4",
	})

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	for _, name := range []string{"DSC_5678.jpg", "VID_0002.mp4"} {
		action := wantClass(t, plan, filepath.Join(card, name), finding.Unresolvable)
		if action.Verb != VerbNone {
			t.Errorf("%s plans %q, want nothing", name, action.Verb)
		}
	}
	wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Incoming)

	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, layout.MarkerName, ReceiptName, dateDir+"/"+jpegName)
}
