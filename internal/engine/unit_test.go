package engine

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/identity"
	"github.com/stampla/stampla/internal/scanner"
)

func TestTargetBase(t *testing.T) {
	id := identity.Identity{
		Time: time.Date(2026, 7, 3, 15, 7, 27, 0, time.UTC),
		Hash: "0a8c8109",
		Ext:  "nef",
	}
	cases := []struct {
		name      string
		base      string
		groupBase string
		want      string
	}{
		{"the master itself", "DSC_1234.NEF", "DSC_1234", "20260703_150727_0a8c8109.nef"},
		{"a plain sidecar", "DSC_1234.xmp", "DSC_1234", "20260703_150727_0a8c8109.xmp"},
		{"an appended sidecar", "DSC_1234.NEF.xmp", "DSC_1234", "20260703_150727_0a8c8109.nef.xmp"},
		{"a vendor sidecar", "DSC_1234.NEF.nksc", "DSC_1234", "20260703_150727_0a8c8109.nef.nksc"},
		{"a labelled derivative", "DSC_1234-Edit.TIF", "DSC_1234", "20260703_150727_0a8c8109-Edit.tif"},
		{"an underscore label", "DSC_1234_pr.jpg", "DSC_1234", "20260703_150727_0a8c8109_pr.jpg"},
		{
			"a canonical name keeps its parts",
			"20200101_010101_deadbeef-Edit.tif", "20200101_010101_deadbeef",
			"20260703_150727_0a8c8109-Edit.tif",
		},
		{
			"a canonical appended sidecar",
			"20200101_010101_deadbeef.nef.xmp", "20200101_010101_deadbeef",
			"20260703_150727_0a8c8109.nef.xmp",
		},
		{
			"a canonical name with a shouted extension",
			"20200101_010101_deadbeef.JPG", "20200101_010101_deadbeef",
			"20260703_150727_0a8c8109.jpg",
		},
		{
			"a member that does not carry its group's base",
			"stray.xmp", "DSC_1234", "20260703_150727_0a8c8109.xmp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := targetBase(tc.base, tc.groupBase, id); got != tc.want {
				t.Errorf("targetBase(%q, %q) = %q, want %q", tc.base, tc.groupBase, got, tc.want)
			}
		})
	}
}

// Only the prefix ever changes: whatever targetBase produces must carry
// the identity's own prefix and nothing of the old one.
func TestTargetBaseOnlyRewritesThePrefix(t *testing.T) {
	id := identity.Identity{
		Time: time.Date(2026, 7, 3, 15, 7, 27, 0, time.UTC),
		Hash: "0a8c8109", Ext: "nef",
	}
	for _, base := range []string{
		"DSC_1234.NEF", "DSC_1234-Edit.tif", "20200101_010101_deadbeef.nef.xmp",
	} {
		got := targetBase(base, "DSC_1234", id)
		if !strings.HasPrefix(got, id.Prefix()) {
			t.Errorf("targetBase(%q) = %q, which does not start with %q", base, got, id.Prefix())
		}
		if strings.Contains(got, "deadbeef") {
			t.Errorf("targetBase(%q) = %q kept the old identity", base, got)
		}
	}
}

func TestChainTags(t *testing.T) {
	got := chainTags()
	want := []string{"CreateDate", "DateCreated", "DateTimeOriginal"}
	if !slices.Equal(got, want) {
		t.Errorf("chainTags() = %v, want %v", got, want)
	}
	// Bare names only: a group-qualified one narrows the read to one
	// group, and ranking needs to see them all.
	for _, tag := range got {
		if strings.ContainsAny(tag, ":@") {
			t.Errorf("chainTags() returned a qualified name: %q", tag)
		}
	}
}

func TestMasterOf(t *testing.T) {
	items := func(paths ...string) []scanner.Item {
		out := make([]scanner.Item, len(paths))
		for i, path := range paths {
			out[i] = scanner.Item{Path: path}
		}
		return out
	}
	cases := []struct {
		name    string
		members []scanner.Item
		want    bool
	}{
		{"a raw master", items("/a/DSC_1234.NEF", "/a/DSC_1234.xmp"), true},
		{"a lone jpeg", items("/a/DSC_1234.jpg"), true},
		{"a canonical master", items("/a/20260703_150727_0a8c8109.nef"), true},
		{"orphan sidecars", items("/a/DSC_1234.xmp"), false},
		{"an orphan derivative", items("/a/20260703_150727_0a8c8109-Edit.tif"), false},
		{"an orphan appended sidecar", items("/a/20260703_150727_0a8c8109.nef.xmp"), false},
		{"nothing at all", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := masterOf(tc.members); got != tc.want {
				t.Errorf("masterOf(%v) = %v, want %v", tc.members, got, tc.want)
			}
		})
	}
}

func TestMemberTargetDir(t *testing.T) {
	home := filepath.Join("/", "card", "DCIM")
	target := filepath.Join("/", "archive", "2026")
	cases := []struct {
		name   string
		member string
		want   string
		ok     bool
	}{
		{"beside the master", filepath.Join(home, "DSC_1234.xmp"), target, true},
		{
			"in a vendor sidecar directory",
			filepath.Join(home, "NKSC_PARAM", "DSC_1234.NEF.nksc"),
			filepath.Join(target, "NKSC_PARAM"), true,
		},
		{"somewhere else entirely", filepath.Join("/", "elsewhere", "DSC_1234.xmp"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := memberTargetDir(home, tc.member, target)
			if ok != tc.ok || got != tc.want {
				t.Errorf("memberTargetDir(%q) = %q, %v; want %q, %v", tc.member, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestGroupClass(t *testing.T) {
	action := func(class finding.Class) Action { return Action{Class: class} }
	cases := []struct {
		name    string
		actions []Action
		want    finding.Class
	}{
		{"all converged", []Action{action(finding.Converged), action(finding.Converged)}, finding.Converged},
		{"one stale", []Action{action(finding.Converged), action(finding.Stale)}, finding.Stale},
		{"a conflict outranks pending", []Action{action(finding.Stale), action(finding.Conflict)}, finding.Conflict},
		{"an alarm outranks everything", []Action{action(finding.Conflict), action(finding.Corrupt)}, finding.Corrupt},
		{"time drift is an alarm too", []Action{action(finding.Converged), action(finding.TimeDrift)}, finding.TimeDrift},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := groupClass(tc.actions); got != tc.want {
				t.Errorf("groupClass = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsIdentityPrefix(t *testing.T) {
	cases := map[string]bool{
		"20260703_150727_0a8c8109": true,
		"20260231_150727_0a8c8109": false, // an impossible date is not an identity
		"20260703_150727_0A8C8109": false, // an uppercase slice is not canonical
		"DSC_1234":                 false,
		"":                         false,
		"20260703_150727":          false,
	}
	for base, want := range cases {
		if got := isIdentityPrefix(base); got != want {
			t.Errorf("isIdentityPrefix(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestIsDAMArtifact(t *testing.T) {
	cases := map[string]bool{
		"Lightroom Catalog.lrcat":      true,
		"Lightroom Catalog.lrcat-data": true,
		"LIGHTROOM CATALOG.LRCAT":      true,
		"Shoot.cocatalog":              true,
		"Shoot.cosessiondb":            true,
		"Shoot.cosession":              true,
		"2026":                         false,
		"catalog.lrcat.backup":         false,
		"20260703_150727_0a8c8109.jpg": false,
		".stampla":                     false,
	}
	for name, want := range cases {
		if got := isDAMArtifact(name); got != want {
			t.Errorf("isDAMArtifact(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDAMArtifactsLooksBesideAndInside(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "Pictures")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(parent, "Lightroom Catalog.lrcat"), "catalog")
	writeFile(t, filepath.Join(dest, "Shoot.cocatalog"), "catalog")
	writeFile(t, filepath.Join(dest, "ordinary.jpg"), "not a catalog")

	got := damArtifacts(dest)
	want := []string{
		filepath.Join(parent, "Lightroom Catalog.lrcat"),
		filepath.Join(dest, "Shoot.cocatalog"),
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("damArtifacts = %v, want %v", got, want)
	}
}

func TestUnder(t *testing.T) {
	root := filepath.Join("/", "archive")
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(root, "2026", "x.jpg"), true},
		{root, true},
		{filepath.Join("/", "archive-other", "x.jpg"), false},
		{filepath.Join("/", "elsewhere"), false},
		{filepath.Join(root, "..", "elsewhere"), false},
	}
	for _, tc := range cases {
		if got := under(root, tc.path); got != tc.want {
			t.Errorf("under(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
		}
	}
	if caseInsensitiveFS && !under(root, filepath.Join("/", "ARCHIVE", "x.jpg")) {
		t.Error("a case-insensitive platform must treat /ARCHIVE as /archive")
	}
}

func TestContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(root, "2026")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := contained(root, filepath.Join(inside, "2026-07", "x.jpg")); err != nil {
		t.Errorf("a path under the root was refused: %v", err)
	}
	if err := contained(root, filepath.Join(outside, "x.jpg")); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a path outside the root: err %v, want ErrEscapesRoot", err)
	}

	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	err := contained(root, filepath.Join(link, "2026", "x.jpg"))
	if !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a target behind a link out of the root: err %v, want ErrEscapesRoot", err)
	}
	var escape *EscapeError
	if errors.As(err, &escape) && !strings.HasPrefix(escape.Resolved, evalPath(t, outside)) {
		t.Errorf("the refusal does not say where the target resolved to: %+v", escape)
	}
}

func TestScratchNaming(t *testing.T) {
	name := scratchBase(jpegName)
	if !strings.HasPrefix(name, ".") {
		t.Errorf("scratch name %q is not hidden; a half-written file must not look like media", name)
	}
	if !isScratchFor(name, jpegName) {
		t.Errorf("isScratchFor(%q, %q) = false", name, jpegName)
	}
	if isScratchFor(name, videoName) {
		t.Errorf("%q was claimed as a scratch file for %q", name, videoName)
	}
	for _, other := range []string{jpegName, "." + jpegName, "." + jpegName + ".stampla-1"} {
		if isScratchFor(other, jpegName) {
			t.Errorf("%q was mistaken for a scratch file", other)
		}
	}
}

func TestCopyIntoVerifiesAndClaims(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	content := strings.Repeat("photons", 5000)
	writeFile(t, src, content)

	target := filepath.Join(dir, "out", "target.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyInto(src, target); err != nil {
		t.Fatalf("copyInto: %v", err)
	}
	if got := readFile(t, target); got != content {
		t.Error("the copy does not match its source")
	}
	// No scratch file survives a completed copy.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the target directory holds %d entries, want only the target", len(entries))
	}

	// A second copy onto the same name is refused, not merged.
	if err := copyInto(src, target); !errors.Is(err, ErrTargetExists) {
		t.Errorf("copying onto an existing target: err %v, want ErrTargetExists", err)
	}
	if got := readFile(t, target); got != content {
		t.Error("the refused copy modified the target")
	}
}

func TestCopyIntoDropsStaleScratch(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	writeFile(t, src, "content")
	target := filepath.Join(dir, "target.bin")
	stale := filepath.Join(dir, "."+"target.bin"+".stampla-4242.part")
	writeFile(t, stale, "half a file from a run that died")

	if err := copyInto(src, target); err != nil {
		t.Fatalf("copyInto: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale scratch file survived")
	}
}

func TestClaimRenameNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.bin")
	target := filepath.Join(dir, "new.bin")
	writeFile(t, old, "mine")

	if err := claimRename(old, target); err != nil {
		t.Fatalf("claimRename: %v", err)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Error("the source survived the rename")
	}
	if got := readFile(t, target); got != "mine" {
		t.Errorf("the target holds %q", got)
	}

	writeFile(t, old, "mine again")
	if err := claimRename(old, target); !errors.Is(err, ErrTargetExists) {
		t.Errorf("renaming onto an occupied target: err %v, want ErrTargetExists", err)
	}
	if got := readFile(t, target); got != "mine" {
		t.Error("a refused rename overwrote the target")
	}
	if got := readFile(t, old); got != "mine again" {
		t.Error("a refused rename removed the source")
	}
}

func TestCaseOnlyRename(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "PHOTO.JPG")
	target := filepath.Join(dir, "photo.jpg")
	writeFile(t, old, "pixels")

	if got := isCaseOnlyRename(old, target); got != caseInsensitiveFS {
		// The two names are one entry only where lookups fold case.
		t.Logf("isCaseOnlyRename = %v on %s", got, runtime.GOOS)
	}
	if err := claimRename(old, target); err != nil {
		t.Fatalf("claimRename: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "photo.jpg" {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("directory holds %v, want only photo.jpg", names)
	}
	if got := readFile(t, target); got != "pixels" {
		t.Errorf("the renamed file holds %q", got)
	}
}

func TestHashFiles(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	want := make(map[string]string)
	for _, content := range []string{"", "a", strings.Repeat("photons", 100000)} {
		path := filepath.Join(dir, "f"+hex.EncodeToString([]byte(content[:min(len(content), 3)]))+".bin")
		writeFile(t, path, content)
		sum := md5.Sum([]byte(content))
		want[path] = hex.EncodeToString(sum[:])
		paths = append(paths, path)
	}
	paths = append(paths, filepath.Join(dir, "gone.bin"))

	got := hashFiles(paths, 3, nil)
	for path, digest := range want {
		if result := got[path]; result.err != nil || result.digest != digest {
			t.Errorf("%s: %q, %v; want %q", path, result.digest, result.err, digest)
		}
	}
	// An unreadable file carries its own error rather than aborting the
	// batch: the rest of the card still imports.
	if result := got[filepath.Join(dir, "gone.bin")]; result.err == nil {
		t.Error("a missing file was hashed successfully")
	}
	if n := len(hashFiles(nil, 0, nil)); n != 0 {
		t.Errorf("hashFiles(nil) returned %d results", n)
	}
}

func TestHashFilesReportsProgress(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range 5 {
		path := filepath.Join(dir, string(rune('a'+i))+".bin")
		writeFile(t, path, "content")
		paths = append(paths, path)
	}
	var seen []int
	hashFiles(paths, 2, func(phase Phase, done, total int, _ string) {
		if phase != PhaseHash {
			t.Errorf("phase %q, want %q", phase, PhaseHash)
		}
		if total != len(paths) {
			t.Errorf("total %d, want %d", total, len(paths))
		}
		seen = append(seen, done)
	})
	slices.Sort(seen)
	if !slices.Equal(seen, []int{1, 2, 3, 4, 5}) {
		t.Errorf("progress counted %v, want each file once", seen)
	}
}

func TestReceiptVerb(t *testing.T) {
	cases := map[Verb]string{VerbCopy: "cp", VerbMove: "mv", VerbUnlink: "mv"}
	for verb, want := range cases {
		if got := receiptVerb(verb); got != want {
			t.Errorf("receiptVerb(%q) = %q, want %q", verb, got, want)
		}
	}
}

func TestAppendReceiptIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), ReceiptName)
	if err := appendReceipt(path, nil); err != nil {
		t.Errorf("appending nothing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("appending nothing created a receipt")
	}
	for i := range 3 {
		err := appendReceipt(path, []receiptLine{{verb: VerbCopy, old: "/a", new: "/b"}})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	lines := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("receipt has %d lines, want 3", len(lines))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("receipt mode %v, want 0644 — it is meant to be read", info.Mode().Perm())
	}
	if _, err := time.Parse(time.RFC3339, strings.Split(lines[0], "\t")[0]); err != nil {
		t.Errorf("receipt time is not RFC 3339: %v", err)
	}
}

func TestModeString(t *testing.T) {
	for mode, want := range map[Mode]string{
		Copy: "cp", Move: "mv",
		VerifyMembership: "verify-membership", VerifySelf: "verify-self",
	} {
		if got := mode.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", int(mode), got, want)
		}
	}
	if !Copy.mutating() || !Move.mutating() || VerifySelf.mutating() || VerifyMembership.mutating() {
		t.Error("only cp and mv mutate")
	}
}

func TestShortDigest(t *testing.T) {
	if got := short("0a8c8109b53e25ac084c7413f6f181f6"); got != "0a8c8109" {
		t.Errorf("short = %q", got)
	}
	if got := short(""); !strings.Contains(got, "no readable payload") {
		t.Errorf("short(\"\") = %q", got)
	}
}

func TestTouchedFraction(t *testing.T) {
	if got := (&Plan{}).TouchedFraction(); got != 0 {
		t.Errorf("an empty plan reports a fraction of %v", got)
	}
	if got := (&Plan{UnderRoot: 8, Touched: 6}).TouchedFraction(); got != 0.75 {
		t.Errorf("TouchedFraction = %v, want 0.75", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func evalPath(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCrossDevice(t *testing.T) {
	if !crossDevice(syscall.EXDEV) {
		t.Error("EXDEV is not recognized as a cross-filesystem error")
	}
	wrapped := &os.LinkError{Op: "link", Old: "/a", New: "/b", Err: syscall.EXDEV}
	if !crossDevice(wrapped) {
		t.Error("a wrapped EXDEV is not recognized")
	}
	if crossDevice(nil) || crossDevice(os.ErrExist) || crossDevice(errors.New("nope")) {
		t.Error("an unrelated error was read as cross-filesystem")
	}
}

// The two-step exists for filesystems that refuse a rename onto what
// they consider the same entry. It is exercised directly, because the
// filesystem a test runs on decides whether the one-step path ever
// falls through to it.
func TestCaseSwapViaTemp(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "PHOTO.JPG")
	target := filepath.Join(dir, "photo.jpg")
	writeFile(t, old, "pixels")

	if err := caseSwapViaTemp(old, target); err != nil {
		t.Fatalf("caseSwapViaTemp: %v", err)
	}
	if got := readFile(t, target); got != "pixels" {
		t.Errorf("the renamed file holds %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d entries left behind, want 1", len(entries))
	}

	// A second half that cannot land puts the file back where it was.
	writeFile(t, old, "pixels again")
	if err := caseSwapViaTemp(old, dir); err == nil {
		t.Error("renaming onto a directory succeeded")
	}
	if got := readFile(t, old); got != "pixels again" {
		t.Errorf("the source was not restored: %q", got)
	}
}

func TestVerifyLandedRefusesAMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "landed.bin")
	writeFile(t, path, "photons")

	if err := verifyLanded(path, "/src", 7, md5Hex("photons")); err != nil {
		t.Errorf("a matching copy was refused: %v", err)
	}
	err := verifyLanded(path, "/src", 7, md5Hex("different"))
	if err == nil || !strings.Contains(err.Error(), "transfer error") {
		t.Errorf("a digest mismatch: err %v, want a transfer error", err)
	}
	if err := verifyLanded(path, "/src", 8, md5Hex("photons")); err == nil {
		t.Error("a size mismatch was accepted")
	}
	if err := verifyLanded(filepath.Join(dir, "gone"), "/src", 0, ""); err == nil {
		t.Error("a missing copy was accepted")
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		err      error
		sentinel error
		mentions []string
	}{
		{&ExistsError{Path: "/a/b.jpg"}, ErrTargetExists, []string{"/a/b.jpg"}},
		{
			&EscapeError{Path: "/root/x", Resolved: "/elsewhere/x", Root: "/root"},
			ErrEscapesRoot,
			[]string{"/root/x", "/elsewhere/x", "/root"},
		},
		{
			&DAMError{DAM: "lrc", Marker: "/root/.stampla"},
			ErrDAMManaged,
			[]string{"lrc", "/root/.stampla", "--inject"},
		},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.sentinel) {
			t.Errorf("%T does not unwrap to its sentinel", tc.err)
		}
		for _, want := range tc.mentions {
			if !strings.Contains(tc.err.Error(), want) {
				t.Errorf("%T: %q does not mention %q", tc.err, tc.err, want)
			}
		}
	}
	failure := Failure{Key: "20260703_150727_0a8c8109", Err: errors.New("no room")}
	if got := failure.String(); !strings.Contains(got, "no room") {
		t.Errorf("Failure.String() = %q", got)
	}
}

func md5Hex(content string) string {
	sum := md5.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// A directory listing answers about names the way the platform's own
// filesystems do: two spellings are one entry where lookups fold case,
// and two files where they do not.
func TestEntryAtFollowsThePlatform(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "PHOTO.JPG"), "pixels")
	p := &planner{entries: make(map[string][]string)}

	if got, ok := p.entryAt(dir, "PHOTO.JPG"); !ok || got != "PHOTO.JPG" {
		t.Errorf("entryAt(exact) = %q, %v", got, ok)
	}
	got, ok := p.entryAt(dir, "photo.jpg")
	if ok != caseInsensitiveFS {
		t.Errorf("entryAt(folded) = %q, %v; want present=%v on %s",
			got, ok, caseInsensitiveFS, runtime.GOOS)
	}
	if ok && got != "PHOTO.JPG" {
		t.Errorf("entryAt(folded) = %q, want the name as it is spelled", got)
	}
	if _, ok := p.entryAt(dir, "absent.jpg"); ok {
		t.Error("an absent name was reported present")
	}
	if _, ok := p.entryAt(filepath.Join(dir, "no-such-dir"), "x.jpg"); ok {
		t.Error("a name in an unreadable directory was reported present")
	}
}

// The claim used where a filesystem has no hard links: the name is taken
// with an exclusive create, and only this run's own empty placeholder is
// ever renamed over.
func TestClaimViaPlaceholder(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.bin")
	target := filepath.Join(dir, "new.bin")
	writeFile(t, old, "mine")

	if err := claimViaPlaceholder(old, target); err != nil {
		t.Fatalf("claimViaPlaceholder: %v", err)
	}
	if got := readFile(t, target); got != "mine" {
		t.Errorf("the target holds %q", got)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Error("the source survived")
	}

	// An occupied name is refused, and the occupant is untouched.
	writeFile(t, old, "mine again")
	if err := claimViaPlaceholder(old, target); !errors.Is(err, ErrTargetExists) {
		t.Errorf("claiming an occupied name: err %v, want ErrTargetExists", err)
	}
	if got := readFile(t, target); got != "mine" {
		t.Error("a refused claim overwrote the target")
	}
	if got := readFile(t, old); got != "mine again" {
		t.Error("a refused claim removed the source")
	}

	// A claim that cannot be completed leaves no placeholder behind.
	free := filepath.Join(dir, "free.bin")
	if err := claimViaPlaceholder(filepath.Join(dir, "absent.bin"), free); err == nil {
		t.Error("claiming for a missing source succeeded")
	}
	if _, err := os.Lstat(free); !os.IsNotExist(err) {
		t.Error("a failed claim left an empty file at the target")
	}
}

// No file lands on a directory, whatever the platform's rename does
// about existing targets.
func TestClaimRenameRefusesADirectory(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.bin")
	writeFile(t, old, "mine")
	target := filepath.Join(dir, "occupied")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := claimRename(old, target); err == nil {
		t.Fatal("renaming onto a directory succeeded")
	}
	if got := readFile(t, old); got != "mine" {
		t.Error("the source did not survive a refused rename")
	}
	if info, err := os.Lstat(target); err != nil || !info.IsDir() {
		t.Error("the directory was replaced")
	}
}
