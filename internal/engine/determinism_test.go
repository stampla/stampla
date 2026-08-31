package engine

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// Same input state, same plan. It is the third principle made
// mechanical: a preview a user reads is worth nothing unless the run
// that follows it decides the same things.
func TestPlanIsDeterministic(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:        "layout = \"" + pattern + "\"\n",
		dateDir + "/" + jpegName: "@dated.jpg",
		"Misfiled/" + movName:    "@date.mov",
		"Loose/DSC_0001.jpg":     "@dated.jpg",
		"Loose/DSC_0002.mp4":     "@date.mp4",
		"Loose/DSC_0003.jpg":     "@plain.jpg",
		"Loose/DSC_0004.mp4":     "@nodate.mp4",
	})
	testutil.StampJPEG(t, filepath.Join(dest, "Loose", "DSC_0005.jpg"), "2026:01:09 08:00:00")
	testutil.WriteSidecar(t, filepath.Join(dest, "Loose", "DSC_0005.xmp"), "2026:01:09 08:00:00")
	testutil.WriteSidecar(t, filepath.Join(dest, "Loose", "DSC_0001.jpg.xmp"), testutil.JPEGDate)

	resolution := declaredLayout(t, dest)
	for _, mode := range []Mode{Move, Copy, VerifySelf} {
		t.Run(mode.String(), func(t *testing.T) {
			first := mustPlan(t, Options{
				Mode: mode, Scan: scanOf(t, dest), Dest: dest,
				Resolution: resolution, Pool: pool, Workers: 1,
			})
			second := mustPlan(t, Options{
				Mode: mode, Scan: scanOf(t, dest), Dest: dest,
				Resolution: resolution, Pool: pool, Workers: 4,
			})
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("two plans over one tree differ\nfirst:\n%s\nsecond:\n%s",
					dumpPlan(first), dumpPlan(second))
			}
			if !slices.IsSortedFunc(first.Groups, func(a, b GroupPlan) int {
				if a.Key < b.Key {
					return -1
				} else if a.Key > b.Key {
					return 1
				}
				return 0
			}) {
				t.Error("groups are not in key order")
			}
		})
	}
}

// Progress is reported and never printed: this package writes nothing to
// any stream of its own.
func TestProgressIsReported(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"DSC_1234.jpg": "@dated.jpg",
		"VID_0001.mp4": "@date.mp4",
	})

	seen := map[Phase]int{}
	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
		Progress: func(phase Phase, done, total int, _ string) {
			seen[phase]++
			if done > total && total != 0 {
				t.Errorf("%s: done %d of %d", phase, done, total)
			}
		},
	})
	if seen[PhaseRead] == 0 {
		t.Error("planning reported no metadata progress")
	}

	applied := map[Phase]int{}
	mustApply(t, plan, ApplyOptions{
		Progress: func(phase Phase, _, _ int, _ string) { applied[phase]++ },
	})
	for _, phase := range []Phase{PhaseApply, PhaseVerify} {
		if applied[phase] == 0 {
			t.Errorf("applying reported no %s progress", phase)
		}
	}

	// A nil callback is the ordinary case and must cost nothing.
	fresh := t.TempDir()
	quiet := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: fresh,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	mustApply(t, quiet, ApplyOptions{})
}

// Every examined file is accounted for exactly once, and the counts are
// the findings.
func TestCountsMatchTheFindings(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	testutil.Tree(t, dest, map[string]string{
		dateDir + "/" + jpegName: "@dated.jpg",
		"Loose/DSC_0002.mp4":     "@date.mp4",
		"Loose/DSC_0003.jpg":     "@plain.jpg",
	})

	scan := scanOf(t, dest)
	plan := mustPlan(t, Options{
		Mode: Move, Scan: scan, Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	total := 0
	for _, count := range plan.Counts {
		total += count
	}
	if total != len(plan.Findings) {
		t.Errorf("counts total %d, findings %d", total, len(plan.Findings))
	}
	// One finding per action, plus whatever the scan itself could not
	// account for: files that exist but were never seen must never pass
	// as files that were not there.
	actions := 0
	for _, group := range plan.Groups {
		actions += len(group.Actions)
	}
	if want := actions + len(scan.Errors); len(plan.Findings) != want {
		t.Errorf("%d findings, want %d (%d actions and %d scan troubles)",
			len(plan.Findings), want, actions, len(scan.Errors))
	}
}
