package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stampla/stampla/internal/scanner"
	"github.com/stampla/stampla/internal/testutil"
)

// TestRemovablePrefix covers every platform from whichever one the tests
// are running on: the path half of the heuristic takes its GOOS as an
// argument precisely so that a Linux CI machine still checks what a Mac
// would do with a memory card.
func TestRemovablePrefix(t *testing.T) {
	tests := []struct {
		goos       string
		path       string
		wantPrefix string
	}{
		{goos: "darwin", path: "/Volumes/NIKON D850/DCIM/DSC_1234.NEF", wantPrefix: "/Volumes"},
		{goos: "darwin", path: "/Volumes/Photos/2026/x.jpg", wantPrefix: "/Volumes"},
		{goos: "darwin", path: "/Volumes/CARD", wantPrefix: "/Volumes"},
		// The mount directory itself is not a volume.
		{goos: "darwin", path: "/Volumes"},
		{goos: "darwin", path: "/Volumes/"},
		{goos: "darwin", path: "/Users/jkb/Pictures/x.jpg"},
		{goos: "darwin", path: "/media/jkb/CARD/x.jpg"},

		{goos: "linux", path: "/media/jkb/CARD/DCIM/x.nef", wantPrefix: "/media"},
		{goos: "linux", path: "/media/CARD/x.nef", wantPrefix: "/media"},
		{goos: "linux", path: "/run/media/jkb/CARD/x.nef", wantPrefix: "/run/media"},
		{goos: "linux", path: "/mediocre/x.nef"},
		{goos: "linux", path: "/home/jkb/Pictures/x.jpg"},
		{goos: "linux", path: "/Volumes/CARD/x.jpg"},

		// A card reader is an ordinary drive letter, so v0.1 detects
		// nothing here rather than asking about every second disk.
		{goos: "windows", path: `D:\DCIM\DSC_1234.NEF`},
		{goos: "windows", path: "/Volumes/CARD/x.jpg"},

		{goos: "plan9", path: "/Volumes/CARD/x.jpg"},
	}

	for _, tc := range tests {
		prefix, ok := removablePrefix(tc.goos, tc.path)
		if ok != (tc.wantPrefix != "") || prefix != tc.wantPrefix {
			t.Errorf("removablePrefix(%q, %q) = %q, %v; want %q, %v",
				tc.goos, tc.path, prefix, ok, tc.wantPrefix, tc.wantPrefix != "")
		}
	}
}

// TestRemovableRootIgnoresTheBootVolume is the case the path check alone
// cannot answer: macOS mounts the boot volume under /Volumes too, and a
// directory there that nothing is mounted on is part of the disk the
// system is running from.
func TestRemovableRootIgnoresTheBootVolume(t *testing.T) {
	dir := t.TempDir()
	if root := removableRoot("/Volumes", dir); root != "" {
		t.Errorf("removableRoot(/Volumes, %s) = %q, want no volume: nothing is mounted there", dir, root)
	}
}

func TestMountPointOfEndsAtTheFilesystemRoot(t *testing.T) {
	dir := t.TempDir()
	mount := mountPointOf(dir)
	if !under(mount, dir) {
		t.Errorf("mountPointOf(%s) = %s, which is not above it", dir, mount)
	}
}

// TestRemovableSourceIgnoresTheDestination proves an archive that itself
// lives on an external disk does not ask about every rename inside it.
func TestRemovableSourceIgnoresTheDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removable media is not detected on Windows in v0.1")
	}
	dest := t.TempDir()
	testutil.StampJPEG(t, filepath.Join(dest, "DSC_1234.jpg"), testutil.JPEGDate)

	scan, err := scanner.Collect([]string{dest}, scanner.Options{})
	if err != nil {
		t.Fatalf("collecting %s: %v", dest, err)
	}
	if root := removableSource(scan, dest); root != "" {
		t.Errorf("removableSource() = %q for a file already under the destination", root)
	}
}

func TestUnder(t *testing.T) {
	root := filepath.Join(string(os.PathSeparator), "photos")
	tests := []struct {
		path string
		want bool
	}{
		{path: root, want: true},
		{path: filepath.Join(root, "2026", "x.jpg"), want: true},
		{path: filepath.Join(string(os.PathSeparator), "photos-old", "x.jpg")},
		{path: filepath.Join(string(os.PathSeparator), "card", "x.jpg")},
	}
	for _, tc := range tests {
		if got := under(root, tc.path); got != tc.want {
			t.Errorf("under(%s, %s) = %v, want %v", root, tc.path, got, tc.want)
		}
	}
}
