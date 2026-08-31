package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// A master and its sidecars share one prefix and converge as one unit,
// each keeping everything about its own name except that prefix.
func TestGroupConvergesTogether(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	testutil.WriteSidecar(t, filepath.Join(card, "DSC_1234.xmp"), testutil.JPEGDate)
	testutil.WriteSidecar(t, filepath.Join(card, "DSC_1234.jpg.xmp"), testutil.JPEGDate)

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if len(plan.Groups) != 1 {
		t.Fatalf("groups %d, want 1 — a master and its sidecars are one group\n%s",
			len(plan.Groups), dumpPlan(plan))
	}
	want := map[string]string{
		"DSC_1234.jpg":     "20260703_150727_0a8c8109.jpg",
		"DSC_1234.xmp":     "20260703_150727_0a8c8109.xmp",
		"DSC_1234.jpg.xmp": "20260703_150727_0a8c8109.jpg.xmp",
	}
	for old, expected := range want {
		action, ok := classOf(plan, filepath.Join(card, old))
		if !ok {
			t.Fatalf("the plan says nothing about %s\n%s", old, dumpPlan(plan))
		}
		if got := filepath.Base(action.New); got != expected {
			t.Errorf("%s becomes %s, want %s", old, got, expected)
		}
	}

	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, layout.MarkerName, ReceiptName,
		dateDir+"/20260703_150727_0a8c8109.jpg",
		dateDir+"/20260703_150727_0a8c8109.jpg.xmp",
		dateDir+"/20260703_150727_0a8c8109.xmp",
	)
}

// A group that fails halfway is put back the way it was, reported, and
// completed by the next run: a master must never be left split from its
// sidecars while the report says the archive is fine.
func TestGroupRevertsMidway(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	testutil.WriteSidecar(t, filepath.Join(card, "DSC_1234.xmp"), testutil.JPEGDate)

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})

	// Something takes the last member's target between the plan and the
	// apply, and the no-clobber claim is what notices. A directory is
	// the blocker because no file lands on one on any platform, whatever
	// the local rename does about existing targets.
	blocked := plan.Groups[0].Actions[len(plan.Groups[0].Actions)-1].New
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("planting the blocker: %v", err)
	}

	result, err := Apply(plan, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("failures %v, want exactly one", result.Failed)
	}
	if !errors.Is(result.Failed[0].Err, ErrTargetExists) {
		t.Errorf("failure %v, want a no-clobber refusal", result.Failed[0].Err)
	}
	if !result.Failed[0].Reverted {
		t.Error("the group was not reverted")
	}
	if result.Members != 0 {
		t.Errorf("a failed group counted %d landed members", result.Members)
	}
	// Nothing is left behind at all: the member that had already landed
	// was taken back out, and no receipt claims otherwise. The blocker
	// is a directory, which the file listing does not report.
	wantTree(t, dest)
	if _, err := os.Stat(filepath.Join(dest, ReceiptName)); !os.IsNotExist(err) {
		t.Error("a failed group wrote a receipt")
	}

	// Once the blockage is gone the same command completes it.
	if err := os.Remove(blocked); err != nil {
		t.Fatalf("clearing the blocker: %v", err)
	}
	again := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	mustApply(t, again, ApplyOptions{})
	wantTree(t, dest, layout.MarkerName, ReceiptName,
		dateDir+"/20260703_150727_0a8c8109.jpg",
		dateDir+"/20260703_150727_0a8c8109.xmp",
	)
}

// The crash story is re-running the same command. Each of these is a
// state an interrupted run can leave behind.
func TestCrashRecovery(t *testing.T) {
	t.Run("stale scratch file", func(t *testing.T) {
		pool := newPool(t)
		card, dest := t.TempDir(), t.TempDir()
		testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})

		// What a run killed mid-copy leaves in the target directory.
		leftover := filepath.Join(dest, filepath.FromSlash(dateDir),
			"."+jpegName+".stampla-4242.part")
		testutil.WriteFile(t, leftover, []byte("half a photograph"))

		plan := mustPlan(t, Options{
			Mode: Copy, Scan: scanOf(t, card), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Incoming)
		mustApply(t, plan, ApplyOptions{})
		wantTree(t, dest, layout.MarkerName, ReceiptName, dateDir+"/"+jpegName)
	})

	t.Run("completed link claim", func(t *testing.T) {
		pool := newPool(t)
		dest := t.TempDir()
		testutil.Tree(t, dest, map[string]string{"Imported/DSC_1234.jpg": "@dated.jpg"})
		source := filepath.Join(dest, "Imported", "DSC_1234.jpg")
		target := filepath.Join(dest, "Imported", jpegName)
		// The window between a rename's link claim and its unlink: one
		// file, two names.
		if err := os.Link(source, target); err != nil {
			t.Skipf("this filesystem has no hard links: %v", err)
		}

		plan := mustPlan(t, Options{
			Mode: Move, Scan: scanOf(t, dest), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		action := wantClass(t, plan, source, finding.Converged)
		if action.Verb != VerbUnlink {
			t.Fatalf("verb %q, want %q\n%s", action.Verb, VerbUnlink, dumpPlan(plan))
		}
		result := mustApply(t, plan, ApplyOptions{})
		wantTree(t, dest, ReceiptName, "Imported/"+jpegName)
		// Finishing an interrupted rename is a landed member like any
		// other, recorded as the move it completes.
		if len(result.Landed) != 1 || result.Landed[0].Verb != VerbUnlink {
			t.Fatalf("landed %v, want one unlink completion", result.Landed)
		}
		wantLandedMatchesReceipt(t, dest, result)
		if lines := receiptLines(t, dest); lines[0][1] != "mv" || lines[0][2] != source {
			t.Errorf("receipt %v does not record the completed move", lines)
		}
	})

	t.Run("half copied group", func(t *testing.T) {
		pool := newPool(t)
		card, dest := t.TempDir(), t.TempDir()
		testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
		testutil.WriteSidecar(t, filepath.Join(card, "DSC_1234.xmp"), testutil.JPEGDate)

		// The master landed and was verified; the sidecar never did.
		landed := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName)
		testutil.CopyFixture(t, "dated.jpg", landed)
		before, err := os.Stat(landed)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		plan := mustPlan(t, Options{
			Mode: Copy, Scan: scanOf(t, card), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Converged)
		wantClass(t, plan, filepath.Join(card, "DSC_1234.xmp"), finding.Incoming)

		result := mustApply(t, plan, ApplyOptions{})
		if result.Members != 1 {
			t.Errorf("landed %d members, want 1 — the master was already there", result.Members)
		}
		wantTree(t, dest, layout.MarkerName, ReceiptName,
			dateDir+"/"+jpegName, dateDir+"/20260703_150727_0a8c8109.xmp")
		after, err := os.Stat(landed)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !os.SameFile(before, after) {
			t.Error("the already-landed master was written again")
		}
	})
}

// A directory inside the archive that is a link out of it is refused:
// every no-clobber guarantee only ever covered the target name, never
// where the name resolved to.
func TestContainmentRefusesSymlinkEscape(t *testing.T) {
	pool := newPool(t)
	card, dest, outside := t.TempDir(), t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	if err := os.Symlink(outside, filepath.Join(dest, "2026")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	action := wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Conflict)
	if action.Verb != VerbNone {
		t.Errorf("verb %q for an escaping target, want nothing", action.Verb)
	}

	result := mustApply(t, plan, ApplyOptions{})
	if result.Members != 0 {
		t.Errorf("landed %d members through a link out of the archive", result.Members)
	}
	wantTree(t, outside)

	// And the apply-time check stands on its own, for a link that
	// appears after the plan was made.
	fresh := t.TempDir()
	direct := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: fresh,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if err := os.Symlink(outside, filepath.Join(fresh, "2026")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	late, err := Apply(direct, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(late.Failed) != 1 || !errors.Is(late.Failed[0].Err, ErrEscapesRoot) {
		t.Errorf("failures %v, want one containment refusal", late.Failed)
	}
	wantTree(t, outside)
}
