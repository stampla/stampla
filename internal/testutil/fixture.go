package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// What the committed fixtures carry. A test that needs to name the file
// stampla should produce states it from these rather than recomputing
// what the fixtures already fix; testdata/README.md lists the files.
const (
	// JPEGDate is the EXIF DateTimeOriginal and CreateDate of dated.jpg,
	// in ExifTool's printed form.
	JPEGDate = "2026:07:03 15:07:27"
	// VideoDate is the QuickTime CreateDate of date.mp4 and date.mov.
	VideoDate = "2026:07:03 13:07:27"
	// SidecarDate is the exif:DateTimeOriginal of dated.xmp, in the ISO
	// form a sidecar stores rather than the form ExifTool prints.
	SidecarDate = "2026-07-03T15:07:27"

	// JPEGHash is the ImageDataHash of dated.jpg, and of plain.jpg,
	// which is the same pixels with the metadata taken off.
	JPEGHash = "0a8c8109b53e25ac084c7413f6f181f6"
	// VideoHash is the ImageDataHash of all three clips, which carry one
	// payload under three sets of metadata.
	VideoHash = "082746c9eb50c105007100d9371d633a"
)

// fixtureDir is the testdata directory beside this file, resolved from
// the source path the compiler recorded rather than from the working
// directory: a helper is called from tests in other packages, and t.Chdir
// moves even the package's own directory out from under a test.
var fixtureDir = locateFixtures()

func locateFixtures() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		// Only reachable in a binary built without the file table. The
		// relative path is still right for a test run from here.
		return "testdata"
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}

// Fixture returns the content of a committed fixture. Only its bytes are
// handed out, never its path: a committed fixture a test can reach is a
// committed fixture a test will eventually rewrite.
func Fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := readFixture(name)
	if err != nil {
		t.Fatalf("testutil: reading fixture %s: %v", name, err)
	}
	return content
}

func readFixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(fixtureDir, name))
}

// CopyFixture writes a copy of a fixture at destPath, creating the
// parent directories it needs. The copy is the test's to mutate.
func CopyFixture(t *testing.T, name, destPath string) {
	t.Helper()
	WriteFile(t, destPath, Fixture(t, name))
}

// WriteFile writes content at path, creating the parent directories.
func WriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("testutil: creating %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("testutil: writing %s: %v", path, err)
	}
}
