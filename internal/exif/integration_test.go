package exif

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pixelJPEG is a 1x1 baseline JPEG: the smallest file every fixture
// here is built from.
var pixelJPEG = []byte{
	0xff, 0xd8, 0xff, 0xdb, 0x00, 0x84, 0x00, 0x10, 0x0b, 0x0c, 0x0e, 0x0c, 0x0a, 0x10, 0x0e, 0x0d,
	0x0e, 0x12, 0x11, 0x10, 0x13, 0x18, 0x28, 0x1a, 0x18, 0x16, 0x16, 0x18, 0x31, 0x23, 0x25, 0x1d,
	0x28, 0x3a, 0x33, 0x3d, 0x3c, 0x39, 0x33, 0x38, 0x37, 0x40, 0x48, 0x5c, 0x4e, 0x40, 0x44, 0x57,
	0x45, 0x37, 0x38, 0x50, 0x6d, 0x51, 0x57, 0x5f, 0x62, 0x67, 0x68, 0x67, 0x3e, 0x4d, 0x71, 0x79,
	0x70, 0x64, 0x78, 0x5c, 0x65, 0x67, 0x63, 0x01, 0x11, 0x12, 0x12, 0x18, 0x15, 0x18, 0x2f, 0x1a,
	0x1a, 0x2f, 0x63, 0x42, 0x38, 0x42, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x00, 0x01, 0x00, 0x01, 0x03, 0x01, 0x22,
	0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01, 0xff, 0xc4, 0x01, 0xa2, 0x00, 0x00, 0x01, 0x05, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03,
	0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04,
	0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05,
	0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1,
	0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a,
	0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38,
	0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58,
	0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78,
	0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97,
	0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5,
	0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3,
	0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9,
	0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0x01, 0x00, 0x03, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03,
	0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x11, 0x00, 0x02, 0x01, 0x02, 0x04, 0x04, 0x03,
	0x04, 0x07, 0x05, 0x04, 0x04, 0x00, 0x01, 0x02, 0x77, 0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05,
	0x21, 0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71, 0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42,
	0x91, 0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33, 0x52, 0xf0, 0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24,
	0x34, 0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37,
	0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57,
	0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77,
	0x78, 0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95,
	0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3,
	0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca,
	0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8,
	0xe9, 0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x00, 0x0c, 0x03,
	0x01, 0x00, 0x02, 0x11, 0x03, 0x11, 0x00, 0x3f, 0x00, 0x86, 0x8a, 0x28, 0xaf, 0x50, 0xf3, 0x8f,
	0xff, 0xd9,
}

// requireExifTool skips when ExifTool is not installed. An ExifTool
// that is installed but unusable is a failure, not a reason to skip
// every test below it.
func requireExifTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool is not on PATH")
	}
	if err := Available(); err != nil {
		t.Fatalf("exiftool is installed but stampla cannot use it: %v", err)
	}
}

// dateTags is the list the engine reads for capture-time ranking:
// bare names, so every group's copy of each comes back.
var dateTags = []string{"DateTimeOriginal", "CreateDate", "DateCreated"}

// exifTool gates the test and hands back a pool closed on cleanup.
func exifTool(t *testing.T, size int) *Pool {
	t.Helper()
	requireExifTool(t)
	pool, err := NewPool(size)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return pool
}

// fixture writes a copy of the pixel JPEG and stamps the given tags
// onto it with ExifTool itself.
func fixture(t *testing.T, dir, name string, tags ...string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, pixelJPEG, 0o600); err != nil {
		t.Fatalf("writing %s: %v", file, err)
	}
	if len(tags) > 0 {
		write(t, file, tags...)
	}
	return file
}

// write stamps tags onto an existing fixture.
func write(t *testing.T, file string, tags ...string) {
	t.Helper()
	args := append([]string{"-overwrite_original", "-q"}, tags...)
	out, err := exec.Command("exiftool", append(args, file)...).CombinedOutput()
	if err != nil {
		t.Fatalf("stamping %s: %v\n%s", file, err, out)
	}
}

func digest(t *testing.T, file string) string {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestAvailableAcceptsTheInstalledExifTool(t *testing.T) {
	requireExifTool(t)
	// The version-only fallback, for a machine with no writable
	// temporary directory, must agree with the probe.
	exe, err := exec.LookPath("exiftool")
	if err != nil {
		t.Fatalf("LookPath: %v", err)
	}
	if err := probeVersion(exe); err != nil {
		t.Errorf("probeVersion: %v", err)
	}
}

func TestReadReturnsGroupQualifiedTags(t *testing.T) {
	pool := exifTool(t, 2)
	dir := t.TempDir()
	file := fixture(t, dir, "DSC_1234.jpg",
		"-DateTimeOriginal=2026:07:03 15:07:27",
		"-Make=Stampla Test",
		"-Artist=Nobody")

	md := pool.Read([]string{file}, []string{"DateTimeOriginal", "Make", "FileType"})[0]
	if md.Err != nil {
		t.Fatalf("Read: %v", md.Err)
	}
	// Only the group name tells a maker-notes time from a QuickTime
	// one, so an unqualified key is a bug, not a convenience.
	for name := range md.Tags {
		if !strings.Contains(name, ":") {
			t.Errorf("tag %q came back without its group", name)
		}
	}
	want := map[string]string{
		"EXIF:DateTimeOriginal": "2026:07:03 15:07:27",
		"EXIF:Make":             "Stampla Test",
		"File:FileType":         "JPEG",
	}
	for name, value := range want {
		if got := md.Tags[name]; got != value {
			t.Errorf("Tags[%q] = %q, want %q", name, got, value)
		}
	}
	if _, ok := md.Tags["SourceFile"]; ok {
		t.Error("SourceFile is in the tag map")
	}
	if len(md.ImageDataHash) != 32 || strings.ToLower(md.ImageDataHash) != md.ImageDataHash {
		t.Errorf("ImageDataHash = %q, want 32 lowercase hex digits", md.ImageDataHash)
	}
}

func TestImageDataHashSurvivesAMetadataRewrite(t *testing.T) {
	// The identity scheme rests on this: writing metadata must never
	// change what a file is, while editing its pixels must.
	pool := exifTool(t, 1)
	dir := t.TempDir()
	file := fixture(t, dir, "steady.jpg", "-DateTimeOriginal=2026:07:03 15:07:27")

	before := pool.Read([]string{file}, dateTags)[0]
	if before.Err != nil {
		t.Fatalf("Read: %v", before.Err)
	}
	if before.ImageDataHash == "" {
		t.Fatal("no ImageDataHash for a JPEG")
	}
	bytesBefore := digest(t, file)

	write(t, file,
		"-DateTimeOriginal=2001:02:03 04:05:06",
		"-Artist=Someone Else",
		"-UserComment=a much longer comment than was there before")

	after := pool.Read([]string{file}, dateTags)[0]
	if after.Err != nil {
		t.Fatalf("Read after the rewrite: %v", after.Err)
	}
	if digest(t, file) == bytesBefore {
		t.Fatal("the rewrite did not change the file, so the test proves nothing")
	}
	if after.Tags["EXIF:DateTimeOriginal"] == before.Tags["EXIF:DateTimeOriginal"] {
		t.Fatal("the rewrite did not change the metadata, so the test proves nothing")
	}
	if after.ImageDataHash != before.ImageDataHash {
		t.Errorf("ImageDataHash changed on a metadata rewrite: %s then %s",
			before.ImageDataHash, after.ImageDataHash)
	}

	// Same payload under another name hashes the same; a payload edit
	// does not.
	copied := fixture(t, dir, "copy.jpg", "-Artist=Third Party")
	edited := filepath.Join(dir, "edited.jpg")
	payload := make([]byte, len(pixelJPEG))
	copy(payload, pixelJPEG)
	payload[len(payload)-3] ^= 0x0f // inside the entropy-coded scan
	if err := os.WriteFile(edited, payload, 0o600); err != nil {
		t.Fatalf("writing the edited fixture: %v", err)
	}

	got := pool.Read([]string{copied, edited}, dateTags)
	if got[0].Err != nil || got[1].Err != nil {
		t.Fatalf("Read: %v, %v", got[0].Err, got[1].Err)
	}
	if got[0].ImageDataHash != before.ImageDataHash {
		t.Errorf("the same payload under another name hashed differently: %s vs %s",
			got[0].ImageDataHash, before.ImageDataHash)
	}
	if got[1].ImageDataHash == before.ImageDataHash {
		t.Error("an edited payload hashed the same as the original")
	}
}

func TestReadKeepsOrderAroundAMissingPath(t *testing.T) {
	pool := exifTool(t, 3)
	dir := t.TempDir()

	paths := make([]string, 0, 7)
	for i := range 6 {
		paths = append(paths, fixture(t, dir, fmt.Sprintf("photo%d.jpg", i),
			fmt.Sprintf("-DateTimeOriginal=2026:07:%02d 15:07:27", i+1)))
	}
	gone := filepath.Join(dir, "never-existed.jpg")
	paths = append(paths[:3:3], append([]string{gone}, paths[3:]...)...)

	got := pool.Read(paths, dateTags)
	if len(got) != len(paths) {
		t.Fatalf("Read returned %d results for %d paths", len(got), len(paths))
	}
	for i, md := range got {
		if md.Path != paths[i] {
			t.Fatalf("result %d is for %q, want %q", i, md.Path, paths[i])
		}
		if i == 3 {
			continue
		}
		if md.Err != nil {
			t.Errorf("result %d (%s): %v", i, md.Path, md.Err)
		}
		day := fmt.Sprintf("2026:07:%02d 15:07:27", indexOfPhoto(t, md.Path)+1)
		if md.Tags["EXIF:DateTimeOriginal"] != day {
			t.Errorf("result %d holds another file's tags: %q", i, md.Tags["EXIF:DateTimeOriginal"])
		}
	}
	if got[3].Err == nil {
		t.Fatal("a path that does not exist came back without an error")
	}
	if !strings.Contains(got[3].Err.Error(), "not found") {
		t.Errorf("error for a missing file = %v, want the reason ExifTool gave", got[3].Err)
	}
}

func indexOfPhoto(t *testing.T, file string) int {
	t.Helper()
	var i int
	if _, err := fmt.Sscanf(filepath.Base(file), "photo%d.jpg", &i); err != nil {
		t.Fatalf("unexpected fixture name %q", file)
	}
	return i
}

func TestReadNeverModifiesAFile(t *testing.T) {
	pool := exifTool(t, 2)
	dir := t.TempDir()
	files := []string{
		fixture(t, dir, "one.jpg", "-DateTimeOriginal=2026:07:03 15:07:27"),
		fixture(t, dir, "two.jpg"),
	}
	before := make([]string, len(files))
	stamps := make([]int64, len(files))
	for i, file := range files {
		before[i] = digest(t, file)
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		stamps[i] = info.ModTime().UnixNano()
	}

	for range 3 {
		pool.Read(files, dateTags)
	}

	for i, file := range files {
		if got := digest(t, file); got != before[i] {
			t.Errorf("%s changed content during a read", file)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.ModTime().UnixNano() != stamps[i] {
			t.Errorf("%s changed modification time during a read", file)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatalf("reading the fixture directory: %v", err)
	} else if len(entries) != len(files) {
		t.Errorf("a read left %d files behind in the directory", len(entries)-len(files))
	}
}

func TestReadDistinguishesNoHashFromAFailure(t *testing.T) {
	pool := exifTool(t, 1)
	dir := t.TempDir()

	sidecar := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(sidecar, []byte("no payload here\n"), 0o600); err != nil {
		t.Fatalf("writing the sidecar: %v", err)
	}
	empty := filepath.Join(dir, "empty.jpg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("writing the empty file: %v", err)
	}

	got := pool.Read([]string{sidecar, empty}, []string{"FileType"})

	// A format with no hashable payload is readable, not broken: the
	// caller falls back to a whole-file hash.
	if got[0].Err != nil {
		t.Errorf("a file with no image data reported an error: %v", got[0].Err)
	}
	if got[0].ImageDataHash != "" {
		t.Errorf("ImageDataHash = %q for a file with no image data", got[0].ImageDataHash)
	}
	if got[0].Tags["File:FileType"] != "TXT" {
		t.Errorf("tags = %v, want them read anyway", got[0].Tags)
	}

	if got[1].Err == nil {
		t.Error("an empty file came back without an error")
	}
}

func TestReadShardsALargeBatch(t *testing.T) {
	pool := exifTool(t, 4)
	dir := t.TempDir()
	const count = 40
	paths := make([]string, count)
	for i := range paths {
		paths[i] = fixture(t, dir, fmt.Sprintf("batch%02d.jpg", i),
			fmt.Sprintf("-Artist=number %d", i))
	}

	got := pool.Read(paths, []string{"Artist"})
	for i, md := range got {
		if md.Path != paths[i] {
			t.Fatalf("result %d is for %q, want %q", i, md.Path, paths[i])
		}
		if md.Err != nil {
			t.Fatalf("result %d: %v", i, md.Err)
		}
		if want := fmt.Sprintf("number %d", i); md.Tags["EXIF:Artist"] != want {
			t.Errorf("result %d holds %q, want %q", i, md.Tags["EXIF:Artist"], want)
		}
		if md.ImageDataHash == "" {
			t.Errorf("result %d has no ImageDataHash", i)
		}
	}
}

func TestReadAwkwardNames(t *testing.T) {
	pool := exifTool(t, 2)
	dir := t.TempDir()
	names := []string{
		"a space and 'quotes'.jpg",
		"Ärchiv ø 2026.jpg",
		"semi;colon & ampersand.jpg",
		"$(not a command).jpg",
	}
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = fixture(t, dir, name, fmt.Sprintf("-Artist=number %d", i))
	}

	for i, md := range pool.Read(paths, []string{"Artist"}) {
		if md.Err != nil {
			t.Errorf("%s: %v", names[i], md.Err)
			continue
		}
		if want := fmt.Sprintf("number %d", i); md.Tags["EXIF:Artist"] != want {
			t.Errorf("%s holds %q, want %q", names[i], md.Tags["EXIF:Artist"], want)
		}
	}
}

func TestReadAPathThatLooksLikeAnOption(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "-overwrite_original.jpg", "-Artist=dashed")
	other := fixture(t, dir, "bystander.jpg", "-Artist=bystander")
	// A process inherits the working directory it was started in, so
	// the move has to happen before the pool does.
	t.Chdir(dir)
	pool := exifTool(t, 1)

	got := pool.Read([]string{"-overwrite_original.jpg", filepath.Base(other)}, []string{"Artist"})
	if got[0].Err != nil {
		t.Fatalf("a file named like an option: %v", got[0].Err)
	}
	if got[0].Tags["EXIF:Artist"] != "dashed" {
		t.Errorf("tags = %v, want the dashed file's own", got[0].Tags)
	}
	if got[1].Err != nil || got[1].Tags["EXIF:Artist"] != "bystander" {
		t.Errorf("the bystander was disturbed: %v %v", got[1].Err, got[1].Tags["EXIF:Artist"])
	}
}

func TestReadRefusesANewlinePathBesideRealFiles(t *testing.T) {
	pool := exifTool(t, 1)
	dir := t.TempDir()
	file := fixture(t, dir, "keep.jpg", "-DateTimeOriginal=2026:07:03 15:07:27")

	smuggled := file + "\n-overwrite_original\n-DateTimeOriginal=1999:01:01 00:00:00\n" + file
	got := pool.Read([]string{file, smuggled}, dateTags)

	if got[0].Err != nil {
		t.Fatalf("the real file failed: %v", got[0].Err)
	}
	if got[1].Err == nil {
		t.Fatal("a path with a newline was accepted")
	}
	// The refusal must have kept the smuggled write out of the batch.
	after := pool.Read([]string{file}, dateTags)[0]
	if after.Tags["EXIF:DateTimeOriginal"] != "2026:07:03 15:07:27" {
		t.Errorf("the file's date became %q", after.Tags["EXIF:DateTimeOriginal"])
	}
}

func TestPoolDefaultSize(t *testing.T) {
	pool := exifTool(t, 0)
	if len(pool.workers) < 1 || len(pool.workers) > defaultPoolSize {
		t.Errorf("NewPool(0) started %d processes", len(pool.workers))
	}
	md := pool.Read([]string{fixture(t, t.TempDir(), "default.jpg")}, dateTags)[0]
	if md.Err != nil {
		t.Errorf("a default pool could not read: %v", md.Err)
	}
}
