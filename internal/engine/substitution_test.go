package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
	"github.com/stampla/stampla/internal/testutil"
)

// captionedJPEG writes the fixture JPEG with a caption in it — a JPEG
// comment segment, which is one of the places a caption actually lives.
//
// The pixels are untouched, so the file keeps the fixture's capture time
// and its image-data hash and derives exactly the same identity name.
// That is the whole point of the fixture: two files a payload digest
// calls identical, that no person would call interchangeable. The
// helper proves both halves of that, so a test built on it can never
// quietly stop testing anything.
func captionedJPEG(t *testing.T, path, caption string) {
	t.Helper()
	testutil.CopyFixture(t, "dated.jpg", path)
	plain, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fixture copy: %v", err)
	}
	// A COM segment goes straight after the start-of-image marker: 0xFFFE,
	// then its own length, then the text.
	segment := []byte{0xFF, 0xFE, byte((len(caption) + 2) >> 8), byte(len(caption) + 2)}
	captioned := append(append(append([]byte{}, plain[:2]...),
		append(segment, caption...)...), plain[2:]...)
	testutil.WriteFile(t, path, captioned)

	if got := testutil.ImageDataHash(t, path); got != testutil.JPEGHash {
		t.Fatalf("the caption moved the image-data hash to %s, want %s —"+
			" this fixture only means anything while the payload is untouched",
			got, testutil.JPEGHash)
	}
	if string(captioned) == string(plain) {
		t.Fatal("the caption changed nothing about the file")
	}
}

// A payload digest names a photograph. It does not say that one file can
// stand in for another: captions, keywords and copyright live in the
// metadata it deliberately excludes. Two files it calls identical are
// two different files, and converging them would discard one of them.
func TestPayloadEqualityIsNotFileEquality(t *testing.T) {
	// The reviewer's first scenario: one card holding the plain frame and
	// the same frame with an evening's captioning on it.
	t.Run("two sources in one run", func(t *testing.T) {
		pool := newPool(t)
		card, dest := t.TempDir(), t.TempDir()
		testutil.Tree(t, card, map[string]string{"a/DSC_0001.jpg": "@dated.jpg"})
		keyworded := filepath.Join(card, "b", "DSC_0002.jpg")
		captionedJPEG(t, keyworded, "Low tide at Vik, the last light")
		before := readBytes(t, keyworded)

		plan := mustPlan(t, Options{
			Mode: Move, Scan: scanOf(t, card), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		action := wantClass(t, plan, keyworded, finding.Conflict)
		if !strings.Contains(action.Detail, "same image data") ||
			!strings.Contains(action.Detail, "metadata") {
			t.Errorf("detail %q does not name what the two files disagree about", action.Detail)
		}
		if action.Verb != VerbNone {
			t.Errorf("verb %q, want nothing", action.Verb)
		}
		if plan.ExitCode() != finding.ExitPending {
			t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
		}
		wantFinding(t, plan, keyworded, finding.Conflict)
		if plan.Counts[finding.Converged] != 0 {
			t.Errorf("%d files were counted converged; neither of these is",
				plan.Counts[finding.Converged])
		}

		result := mustApply(t, plan, ApplyOptions{})
		if len(result.Landed) != 1 {
			t.Errorf("landed %v, want only the plain frame", result.Landed)
		}
		if got := readBytes(t, keyworded); got != before {
			t.Error("the captioned source was modified")
		}
	})

	// The reviewer's second scenario: the plain frame is imported, and
	// the captioned one is moved in afterwards.
	t.Run("occupied target", func(t *testing.T) {
		pool := newPool(t)
		dest := t.TempDir()
		testutil.CopyFixture(t, "dated.jpg",
			filepath.Join(dest, filepath.FromSlash(dateDir), jpegName))

		card := t.TempDir()
		keyworded := filepath.Join(card, "DSC_0002.jpg")
		captionedJPEG(t, keyworded, "Low tide at Vik, the last light")
		before := readBytes(t, keyworded)

		plan := mustPlan(t, Options{
			Mode: Move, Scan: scanOf(t, card), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		action := wantClass(t, plan, keyworded, finding.Conflict)
		if !strings.Contains(action.Detail, "same image data") {
			t.Errorf("detail %q does not name what the two files disagree about", action.Detail)
		}
		if plan.ExitCode() != finding.ExitPending {
			t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
		}
		wantFinding(t, plan, keyworded, finding.Conflict)

		result := mustApply(t, plan, ApplyOptions{})
		if len(result.Landed) != 0 {
			t.Errorf("landed %v, want nothing", result.Landed)
		}
		if got := readBytes(t, keyworded); got != before {
			t.Error("the captioned source was modified")
		}
		wantTree(t, dest, dateDir+"/"+jpegName)
	})

	// And the same question asked by the membership check, whose exit
	// code is what a person reads before formatting the card.
	t.Run("membership", func(t *testing.T) {
		pool := newPool(t)
		dest := t.TempDir()
		testutil.CopyFixture(t, "dated.jpg",
			filepath.Join(dest, filepath.FromSlash(dateDir), jpegName))

		card := t.TempDir()
		keyworded := filepath.Join(card, "DSC_0002.jpg")
		captionedJPEG(t, keyworded, "Low tide at Vik, the last light")

		plan := mustPlan(t, Options{
			Mode: VerifyMembership, Scan: scanOf(t, card), Dest: dest,
			Resolution: fallbackLayout(t), Pool: pool,
		})
		wantClass(t, plan, keyworded, finding.Conflict)
		if plan.ExitCode() != finding.ExitPending {
			t.Errorf("exit code %d, want %d — the archive does not hold this file",
				plan.ExitCode(), finding.ExitPending)
		}
	})
}

// Byte-identical really is converged: the dedup that makes re-importing
// a card a no-op has to keep working, or the fix would have traded one
// wrong answer for another.
func TestByteIdenticalStillConverges(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"a/DSC_0001.jpg": "@dated.jpg",
		"b/DSC_0002.jpg": "@dated.jpg",
	})

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	second := wantClass(t, plan, filepath.Join(card, "b", "DSC_0002.jpg"), finding.Converged)
	if !strings.Contains(second.Detail, "converged once") {
		t.Errorf("detail %q does not say it converges once", second.Detail)
	}
	mustApply(t, plan, ApplyOptions{})
	wantTree(t, dest, layout.MarkerName, ReceiptName, dateDir+"/"+jpegName)

	// And again against what is now on disk.
	again := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if again.Mutations() != 0 || again.ExitCode() != finding.ExitConverged {
		t.Errorf("re-import plans %d mutations at exit %d\n%s",
			again.Mutations(), again.ExitCode(), dumpPlan(again))
	}
}

// A group is a duplicate only when every one of its members is. A
// sidecar full of edits beside an identical master is not a group
// somebody already has.
func TestDuplicateNeedsEveryMember(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"a/DSC_0001.jpg": "@dated.jpg",
		"b/DSC_0002.jpg": "@dated.jpg",
	})
	testutil.WriteSidecar(t, filepath.Join(card, "b", "DSC_0002.xmp"), testutil.JPEGDate)

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	action := wantClass(t, plan, filepath.Join(card, "b", "DSC_0002.jpg"), finding.Conflict)
	if action.Verb != VerbNone {
		t.Errorf("verb %q, want nothing", action.Verb)
	}
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d", plan.ExitCode(), finding.ExitPending)
	}
}

// The membership check accounts for every regular file on the card, not
// only the ones stampla owns a format for. Silence about a file is the
// failure mode the tool exists to prevent, and this exit code is read by
// somebody about to erase the original.
func TestVerifyMembershipAccountsForUnownedFormats(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"DSC_1234.jpg": "@dated.jpg",
		"notes.txt":    "the shoot list",
		"sub/scan.pdf": "a model release",
	})
	testutil.Tree(t, dest, map[string]string{
		layout.MarkerName:        "layout = \"" + pattern + "\"\n",
		dateDir + "/" + jpegName: "@dated.jpg",
	})

	scan := scanOfKeepingUnowned(t, card)
	plan := mustPlan(t, Options{
		Mode: VerifyMembership, Scan: scan, Dest: dest,
		Resolution: declaredLayout(t, dest), Pool: pool,
	})
	wantClass(t, plan, filepath.Join(card, "DSC_1234.jpg"), finding.Converged)
	for _, name := range []string{"notes.txt", filepath.Join("sub", "scan.pdf")} {
		path := filepath.Join(card, name)
		action := wantClass(t, plan, path, finding.Unresolvable)
		if !strings.Contains(action.Detail, "format not owned") {
			t.Errorf("%s: detail %q does not say why", name, action.Detail)
		}
		wantFinding(t, plan, path, finding.Unresolvable)
	}
	if plan.ExitCode() != finding.ExitPending {
		t.Errorf("exit code %d, want %d — this card is not fully accounted for",
			plan.ExitCode(), finding.ExitPending)
	}

	// The mutation verbs never collect them, so nothing is ever renamed
	// on the strength of a format stampla owns no identity for.
	if got := scanOf(t, card); len(got.Groups) != 1 {
		t.Errorf("a mutation scan collected %d groups, want 1", len(got.Groups))
	}
}

func readBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// A still and a clip sharing a base name are two groups, and everything
// that reaches back to a group from a result has to tell them apart. The
// shape is every Live Photo's: IMG_1234.JPG beside IMG_1234.MP4.
func TestSameBasePhotoAndVideoStayApart(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{
		"IMG_1234.jpg": "@dated.jpg",
		"IMG_1234.mp4": "@date.mp4",
	})
	still := filepath.Join(card, "IMG_1234.jpg")
	clip := filepath.Join(card, "IMG_1234.mp4")

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	if len(plan.Groups) != 2 {
		t.Fatalf("groups %d, want 2 — one name, two identities\n%s",
			len(plan.Groups), dumpPlan(plan))
	}
	if plan.Groups[0].Key == plan.Groups[1].Key {
		t.Errorf("both groups answer to %q; a result could not tell them apart",
			plan.Groups[0].Key)
	}
	kinds := map[scanner.Kind]bool{}
	for _, group := range plan.Groups {
		if group.Kind == "" {
			t.Errorf("group %q carries no kind", group.Key)
		}
		kinds[group.Kind] = true
	}
	if !kinds[scanner.KindPhoto] || !kinds[scanner.KindVideo] {
		t.Errorf("kinds %v, want one photo and one video", kinds)
	}

	// Neither claims the other's name, so neither is written off as a
	// duplicate of it: two identities, two targets, two copies.
	wantClass(t, plan, still, finding.Incoming)
	wantClass(t, plan, clip, finding.Incoming)

	// One group is blocked; the other must land, and the failure must
	// not be read as covering the sibling's files.
	blocked := ""
	for _, group := range plan.Groups {
		if group.Kind == scanner.KindVideo {
			blocked = group.Actions[0].New
		}
	}
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
	if len(result.Landed) != 1 || result.Landed[0].Old != still {
		t.Errorf("landed %v, want only the still", result.Landed)
	}

	// The lookup a report does to name the files of a failed group. Keyed
	// on the base alone it would blame the still for the clip's failure.
	failed := map[string]bool{}
	for _, f := range result.Failed {
		failed[f.Key] = true
	}
	blamed := map[string]bool{}
	for _, group := range plan.Groups {
		if failed[group.Key] {
			for _, action := range group.Actions {
				blamed[action.Old] = true
			}
		}
	}
	if !blamed[clip] {
		t.Error("the clip is not among the failed group's files")
	}
	if blamed[still] {
		t.Error("the still was reported as failed; only its namesake clip was")
	}
	wantTree(t, dest, layout.MarkerName, ReceiptName, dateDir+"/"+jpegName)
}
