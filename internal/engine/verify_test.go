package engine

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// The membership check answers one question: is this source accounted
// for at its place in this archive? For a memory card, exit zero is what
// makes it safe to format.
func TestVerifyMembership(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"DSC_1234.jpg": "@dated.jpg",
		"VID_0001.mp4": "@date.mp4",
	})
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:        "layout = \"" + pattern + "\"\n",
		dateDir + "/" + jpegName: "@dated.jpg",
	})

	plan := mustPlan(t, Options{
		Mode: VerifyMembership, Scan: scanOf(t, card), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	present := wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Converged)
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName); present.New != want {
		t.Errorf("expected path %s, want %s", present.New, want)
	}
	missing := wantClass(t, plan, filepath.Join(card, "VID_0001.mp4"), finding.Missing)
	if want := filepath.Join(dest, filepath.FromSlash(dateDir), videoName); missing.New != want {
		t.Errorf("the missing finding must carry where it looked: %s, want %s",
			missing.New, want)
	}
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
	}
	if plan.Mutations() != 0 {
		t.Errorf("a membership check planned %d mutations", plan.Mutations())
	}

	// Once everything is there, the card is accounted for.
	testutil.CopyFixture(t, "date.mp4", filepath.Join(dest, filepath.FromSlash(dateDir), videoName))
	complete := mustPlan(t, Options{
		Mode: VerifyMembership, Scan: scanOf(t, card), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	if complete.ExitCode() != finding.ExitConverged {
		t.Errorf("exit code %d, want %d\n%s",
			complete.ExitCode(), finding.ExitConverged, dumpPlan(complete))
	}
}

// "Accounted for" means the archive holds this file, not a file that
// would be given the same name. The exit code is what a person reads
// before formatting a card, so presence at the right name is not enough
// on its own.
func TestVerifyMembershipComparesContent(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	landed := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName)
	testutil.CopyFixture(t, "dated.jpg", landed)
	corruptPayload(t, landed)

	plan := mustPlan(t, Options{
		Mode: VerifyMembership, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Conflict)
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d — this card is not accounted for",
			plan.ExitCode(), finding.ExitPending)
	}
}

// The self-check classifies everything under the root against its own
// recomputed identity, and changes nothing.
func TestVerifySelf(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:        "layout = \"" + pattern + "\"\n",
		dateDir + "/" + jpegName: "@dated.jpg",
		"Misfiled/" + movName:    "@date.mov",
		"Loose/DSC_0009.jpg":     "@dated.jpg",
		"Loose/DSC_0010.jpg":     "@plain.jpg",
	})
	before := testutil.RelPaths(t, dest)

	plan := mustPlan(t, Options{
		Mode: VerifySelf, Scan: scanOf(t, dest), Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	wantClass(t, plan, filepath.Join(dest, filepath.FromSlash(dateDir), jpegName), finding.Converged)
	wantClass(t, plan, filepath.Join(dest, "Misfiled", movName), finding.Misplaced)
	wantClass(t, plan, filepath.Join(dest, "Loose", "DSC_0010.jpg"), finding.Unresolvable)
	// DSC_0009.jpg holds the same photograph as the one already filed,
	// so its identity name is taken by a file that really is it.
	wantClass(t, plan, filepath.Join(dest, "Loose", "DSC_0009.jpg"), finding.Converged)

	if plan.Mutations() != 0 {
		t.Errorf("a self-check planned %d mutations\n%s", plan.Mutations(), dumpPlan(plan))
	}
	if got := testutil.RelPaths(t, dest); len(got) != len(before) {
		t.Errorf("the tree changed: %v, was %v", got, before)
	}
}

// Applying a plan a verify mode built is refused rather than quietly
// treated as a dry run.
func TestApplyRefusesVerifyPlans(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{"DSC_1234.jpg": "@dated.jpg"})

	for _, mode := range []Mode{VerifySelf, VerifyMembership} {
		plan := mustPlan(t, Options{
			Mode: mode, Scan: scanOf(t, dest), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		if _, err := Apply(plan, ApplyOptions{}); !errors.Is(err, ErrReadOnlyMode) {
			t.Errorf("Apply(%s): err %v, want ErrReadOnlyMode", mode, err)
		}
	}
}

// A destination that is not a directory, and a run with no way to read
// metadata, are refusals rather than findings: neither leaves anything
// to report.
func TestBuildPlanRefusals(t *testing.T) {
	pool := newPool(t)
	dir := t.TempDir()
	testutil.Tree(t, dir, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	scan := scanOf(t, dir)

	if _, err := BuildPlan(Options{
		Mode: Copy, Scan: scan, Dest: filepath.Join(dir, "DSC_1234.jpg"),
		Resolution: fallbackLayout(t), Pool: pool,
	}); !errors.Is(err, ErrNotDir) {
		t.Errorf("a file as the destination: err %v, want ErrNotDir", err)
	}
	if _, err := BuildPlan(Options{
		Mode: Copy, Scan: scan, Dest: dir, Resolution: fallbackLayout(t),
	}); !errors.Is(err, ErrNoPool) {
		t.Errorf("no pool: err %v, want ErrNoPool", err)
	}
	if _, err := BuildPlan(Options{
		Mode: Copy, Dest: dir, Resolution: fallbackLayout(t), Pool: pool,
	}); err == nil {
		t.Error("no scan: want an error")
	}
	if _, err := Apply(nil, ApplyOptions{}); err == nil {
		t.Error("Apply(nil): want an error")
	}
}

// The scan's own troubles are part of the run's report: a directory that
// would not list holds files this run cannot see, and a plan that
// omitted them would call an unreadable card accounted for.
func TestScanErrorsReachTheFindings(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"DSC_1234.jpg": "@dated.jpg"})

	scan := scanOf(t, card, filepath.Join(card, "missing.jpg"))
	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scan, Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if plan.Counts[finding.Missing] != 1 {
		t.Errorf("missing findings %d, want 1: %v", plan.Counts[finding.Missing], plan.Findings)
	}
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
	}
}
