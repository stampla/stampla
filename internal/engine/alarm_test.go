package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/testutil"
)

// A write-once file whose content disagrees with its name is never
// renamed, under any mode. The old name is the only surviving record of
// what the file used to be, and renaming would turn evidence of damage
// into a plausible file.
func TestWriteOnceAlarms(t *testing.T) {
	cases := []struct {
		name    string
		damage  func(t *testing.T, path string)
		class   finding.Class
		mention string
	}{
		{
			name:    "payload corruption",
			damage:  corruptPayload,
			class:   finding.Corrupt,
			mention: "082746c9",
		},
		{
			name: "capture time rewritten",
			damage: func(t *testing.T, path string) {
				t.Helper()
				testutil.StampVideo(t, path, "2026:07:04 10:00:00")
			},
			class:   finding.TimeDrift,
			mention: "20260704_100000",
		},
	}

	for _, tc := range cases {
		for _, mode := range []Mode{Move, Copy, VerifySelf} {
			t.Run(tc.name+"/"+mode.String(), func(t *testing.T) {
				pool := newPool(t)
				dest := t.TempDir()
				damaged := filepath.Join(dest, filepath.FromSlash(dateDir), videoName)
				testutil.CopyFixture(t, "date.mp4", damaged)
				tc.damage(t, damaged)
				before, err := os.ReadFile(damaged)
				if err != nil {
					t.Fatalf("reading the damaged file: %v", err)
				}

				plan := mustPlan(t, Options{
					Mode: mode, Scan: scanOf(t, dest), Dest: dest,
					Resolution: fallbackLayout(t), Pool: pool,
				})
				action := wantClass(t, plan, damaged, tc.class)
				if action.Verb != VerbNone || action.New != "" {
					t.Errorf("an alarm planned %q to %q, want nothing", action.Verb, action.New)
				}
				if !strings.Contains(action.Detail, tc.mention) {
					t.Errorf("detail %q does not carry its evidence (%s)", action.Detail, tc.mention)
				}
				if plan.Alarms() != 1 {
					t.Errorf("Alarms() is %d, want 1", plan.Alarms())
				}
				if plan.ExitCode() != finding.ExitAlarm {
					t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitAlarm)
				}
				if !plan.Groups[0].Refused {
					t.Error("an alarmed group is not refused")
				}

				if mode == VerifySelf {
					return
				}
				result := mustApply(t, plan, ApplyOptions{})
				if result.Members != 0 || len(result.Landed) != 0 {
					t.Errorf("an alarmed group landed %v", result.Landed)
				}
				wantTree(t, dest, dateDir+"/"+videoName)
				if after, err := os.ReadFile(damaged); err != nil || string(after) != string(before) {
					t.Error("the damaged file was modified")
				}
			})
		}
	}
}

// An editable format drifts legitimately: keywords, ratings and pixel
// edits all move its content, so a mismatch renames instead of alarming.
func TestEditableFormatsGoStale(t *testing.T) {
	cases := []struct {
		name   string
		damage func(t *testing.T, path string)
		want   string
	}{
		{
			name:   "pixels edited",
			damage: corruptPayload,
			want:   "20260703_150727_cb923338.jpg",
		},
		{
			name: "capture time re-dated",
			damage: func(t *testing.T, path string) {
				t.Helper()
				testutil.StampJPEG(t, path, "2026:07:04 10:00:00")
			},
			want: "20260704_100000_0a8c8109.jpg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newPool(t)
			dest := t.TempDir()
			testutil.Tree(t, dest, map[string]string{layout.MarkerName: "layout = \"" + pattern + "\"\n"})
			drifted := filepath.Join(dest, filepath.FromSlash(dateDir), jpegName)
			testutil.CopyFixture(t, "dated.jpg", drifted)
			tc.damage(t, drifted)

			plan := mustPlan(t, Options{
				Mode: Move, Scan: scanOf(t, dest), Dest: dest,
				Resolution: declaredLayout(t, dest), Pool: pool,
			})
			action := wantClass(t, plan, drifted, finding.Stale)
			if filepath.Base(action.New) != tc.want {
				t.Errorf("target %s, want %s", filepath.Base(action.New), tc.want)
			}
			if plan.Alarms() != 0 {
				t.Errorf("an editable format raised %d alarms", plan.Alarms())
			}
			if plan.ExitCode() != finding.ExitPending {
				t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
			}

			mustApply(t, plan, ApplyOptions{})
			// A re-dated JPEG belongs in a different month, and the
			// declared layout moves it there.
			relocated := filepath.Dir(strings.TrimPrefix(
				filepath.ToSlash(strings.TrimPrefix(action.New, dest+string(filepath.Separator))), "/"))
			wantTree(t, dest, layout.MarkerName, ReceiptName, relocated+"/"+tc.want)
		})
	}
}

// A sidecar carries no content hash of its own, so it never alarms; it
// follows its master's prefix wherever that goes.
func TestSidecarFollowsAStaleMaster(t *testing.T) {
	pool := newPool(t)
	dest := t.TempDir()
	master := filepath.Join(dest, jpegName)
	testutil.CopyFixture(t, "dated.jpg", master)
	testutil.WriteSidecar(t, filepath.Join(dest, "20260703_150727_0a8c8109.jpg.xmp"), testutil.JPEGDate)
	corruptPayload(t, master)

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, dest), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	wantClass(t, plan, master, finding.Stale)
	sidecar := wantClass(t, plan, filepath.Join(dest, "20260703_150727_0a8c8109.jpg.xmp"), finding.Stale)
	if want := "20260703_150727_cb923338.jpg.xmp"; filepath.Base(sidecar.New) != want {
		t.Errorf("sidecar target %s, want %s", filepath.Base(sidecar.New), want)
	}

	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, ReceiptName,
		"20260703_150727_cb923338.jpg",
		"20260703_150727_cb923338.jpg.xmp")
}
