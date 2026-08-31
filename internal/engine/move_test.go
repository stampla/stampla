package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// On an undeclared root the names converge and the directories do not: a
// fallback layout may place a new file, and may never reorganize a tree
// somebody else arranged.
func TestMoveInPlaceConvergesNamesOnly(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		"Imported/DSC_1234.jpg": "@dated.jpg",
		"Imported/VID_0001.mp4": "@date.mp4",
	})

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	action := wantClass(t, plan, filepath.Join(dest, "Imported", "DSC_1234.jpg"), finding.Incoming)
	if want := filepath.Join(dest, "Imported", jpegName); action.New != want {
		t.Errorf("target %s, want %s — an undeclared layout must not relocate", action.New, want)
	}
	if action.Verb != VerbMove {
		t.Errorf("verb %q, want %q", action.Verb, VerbMove)
	}

	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, ReceiptName, "Imported/"+jpegName, "Imported/"+videoName)

	// Nothing was placed by the layout, so nothing declared it: writing
	// the fallback into the marker would tell the next run to move every
	// one of these files.
	if marker, err := layout.ReadMarker(dest); err != nil || marker != nil {
		t.Errorf("an undeclared in-place rename declared a layout: %v, %v", marker, err)
	}

	again := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if again.Mutations() != 0 {
		t.Errorf("re-run plans %d mutations, want none\n%s", again.Mutations(), dumpPlan(again))
	}
}

// A declared layout is the only kind that relocates, and a correctly
// named file in the wrong directory is exactly what misplaced means.
func TestMoveDeclaredRelocates(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:       "layout = \"" + pattern + "\"\n",
		"Misfiled/" + jpegName:  "@dated.jpg",
		"Misfiled/DSC_0002.mp4": "@date.mp4",
	})

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	misplaced := wantClass(t, plan, filepath.Join(dest, "Misfiled", jpegName), finding.Misplaced)
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName); misplaced.New != want {
		t.Errorf("relocation target %s, want %s", misplaced.New, want)
	}
	// The unnamed one needs both a name and a shelf, which is one move.
	renamed := wantClass(t, plan, filepath.Join(dest, "Misfiled", "DSC_0002.mp4"), finding.Incoming)
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), videoName); renamed.New != want {
		t.Errorf("rename target %s, want %s", renamed.New, want)
	}

	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest,
		layout.MarkerName, ReceiptName,
		dateDir+"/"+jpegName, dateDir+"/"+videoName,
	)
	for _, line := range receiptLines(t, dest) {
		if line[1] != "mv" {
			t.Errorf("receipt verb %q, want mv", line[1])
		}
	}
}

// The mass-relocation tripwire is computed here and acted on nowhere:
// how many files were already under the root, and how many of them the
// plan would move.
func TestTripwireCounts(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName: "layout = \"" + pattern + "\"\n",
		// Two already where they belong, two not.
		dateDir + "/" + jpegName: "@dated.jpg",
		dateDir + "/" + movName:  "@date.mov",
		"Misfiled/DSC_0002.mp4":  "@date.mp4",
		"Misfiled/DSC_0003.jpg":  "@dated.jpg",
	})

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	if plan.UnderRoot != 4 {
		t.Errorf("UnderRoot %d, want 4", plan.UnderRoot)
	}
	// DSC_0002.mp4 moves; DSC_0003.jpg is the same photograph as the one
	// already filed, so it converges to a name that is taken and is left
	// exactly where it is.
	if plan.Touched != 1 {
		t.Errorf("Touched %d, want 1\n%s", plan.Touched, dumpPlan(plan))
	}
	if got := plan.TouchedFraction(); got != 0.25 {
		t.Errorf("TouchedFraction %v, want 0.25", got)
	}

	// A plan that touches nothing already there reports no fraction at
	// all, whatever it imports.
	card := t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1.jpg": "@dated.jpg"})
	fresh := t.TempDir()
	importing := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: fresh,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if importing.UnderRoot != 0 || importing.TouchedFraction() != 0 {
		t.Errorf("an import reported UnderRoot %d and fraction %v, want 0 and 0",
			importing.UnderRoot, importing.TouchedFraction())
	}
}

// Across filesystems a move is a verified copy followed by a delete, in
// that order and never the other: the source goes only once every member
// of its group has been read back at the destination.
func TestMoveAcrossVolumes(t *testing.T) {
	pool := newPool(t)
	source, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, source, map[string]string{
		"DSC_1234.jpg": "@dated.jpg",
		"DSC_1234.xmp": "@dated.xmp",
	})

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, source), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	result := mustApply(t, plan, ApplyOptions{ForceCrossVolume: true})
	if result.Members != 2 {
		t.Errorf("landed %d members, want 2", result.Members)
	}
	wantLandedMatchesReceipt(t, dest, result)
	for _, action := range result.Landed {
		if action.Verb != VerbMove {
			t.Errorf("%s landed as %q, want %q", action.Old, action.Verb, VerbMove)
		}
	}
	wantTree(t, dest,
		layout.MarkerName, ReceiptName,
		dateDir+"/20260703_150727_0a8c8109.jpg",
		dateDir+"/20260703_150727_0a8c8109.xmp",
	)
	wantTree(t, source)
	for _, line := range receiptLines(t, dest) {
		if line[1] != "mv" {
			t.Errorf("receipt verb %q, want mv", line[1])
		}
	}
}

// A name that is canonical apart from the case of its extension is
// renamed, on the filesystems where the two names are one directory
// entry as well as on the ones where they are two.
func TestMoveFixesExtensionCase(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	shouty := "20260703_150727_0a8c8109.JPG"
	testutil.Tree(t, dest, map[string]string{"Imported/" + shouty: "@dated.jpg"})

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	// The name carries an identity — the right one — so it is stale
	// rather than unnamed, even though only its extension case is wrong.
	action := wantClass(t, plan, filepath.Join(dest, "Imported", shouty), finding.Stale)
	if want := filepath.Join(dest, "Imported", jpegName); action.New != want {
		t.Fatalf("target %s, want %s", action.New, want)
	}
	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, ReceiptName, "Imported/"+jpegName)
}

// cp reports a file that already sits under the destination and does
// nothing to it: copying it to a second name would leave the archive
// holding one photograph twice.
func TestCopyLeavesFilesAlreadyUnderTheRoot(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{"Imported/DSC_1234.jpg": "@dated.jpg"})

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, dest), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	action := wantClass(t, plan, filepath.Join(dest, "Imported", "DSC_1234.jpg"), finding.Incoming)
	if action.Verb != VerbNone {
		t.Errorf("cp plans %q for a file already under the root, want nothing", action.Verb)
	}
	result := mustApply(t, plan, ApplyOptions{})
	if result.Members != 0 || len(result.Landed) != 0 {
		t.Errorf("cp landed %v, want nothing", result.Landed)
	}
	wantTree(t, dest, "Imported/DSC_1234.jpg")
}

// Another archive inside this one is not this run's business.
func TestMoveRefusesNestedArchive(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:                   "layout = \"" + pattern + "\"\n",
		"Inner/" + layout.MarkerName:        "layout = \"{yyyy}\"\n",
		"Inner/DSC_1234.jpg":                "@dated.jpg",
		filepath.Join("Loose", "DSC_2.jpg"): "@dated.jpg",
	})

	// The nested root's file has to be named explicitly to reach the
	// plan at all: recursion already stops at the marker.
	inner := filepath.Join(dest, "Inner", "DSC_1234.jpg")
	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, inner), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	action := wantClass(t, plan, inner, finding.Conflict)
	if action.Verb != VerbNone {
		t.Errorf("verb %q for a file in a nested archive, want nothing", action.Verb)
	}
	result := mustApply(t, plan, ApplyOptions{})
	if result.Members != 0 {
		t.Errorf("landed %d members out of a nested archive", result.Members)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Errorf("the nested archive's file was moved: %v", err)
	}
}
