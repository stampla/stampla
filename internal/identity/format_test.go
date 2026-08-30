package identity

import (
	"path/filepath"
	"testing"
)

func TestIsMedia(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"card/DSC_1234.NEF", true},
		{"card/DSC_1234.nef", true},
		{"card/IMG_5678.JPEG", true},
		{"card/IMG_5678.jpg", true},
		{"card/IMG_5678.heic", true},
		{"card/scan.tif", true},
		{"card/scan.tiff", true},
		{"card/edit.dng", true},
		{"card/A001.braw", true},
		{"card/A002.R3D", true},
		{"card/clip.mov", true},
		{"card/clip.mp4", true},
		{"card/clip.mts", true},
		{"card/DSC_1234.xmp", false}, // a sidecar has no identity of its own
		{"card/DSC_1234.nef.xmp", false},
		{"card/DSC_1234.pp3", false},
		{"card/DSC_1234.nksc", false},
		{"card/notes.txt", false},
		{"card/edit.psd", false}, // editable, but never a group's master
		{"card/DSC_1234", false},
		{"", false},
		{"card/.hidden", false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsMedia(filepath.FromSlash(tc.path)); got != tc.want {
				t.Errorf("IsMedia(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsWriteOnce(t *testing.T) {
	// RAW families and camera-original video are never renamed on a
	// mismatch; everything that is edited in place renames.
	writeOnce := []string{
		"nef", "NEF", ".nef", "cr2", "cr3", "raf", "rw2", "arw",
		"mov", "mp4", "m4v", "avi", "mkv", "braw", "nev", "r3d",
		"mts", "m2ts", "3gp", "wmv", "asf", "mpg", "mpeg",
	}
	for _, ext := range writeOnce {
		t.Run("writeonce/"+ext, func(t *testing.T) {
			if !IsWriteOnce(ext) {
				t.Errorf("IsWriteOnce(%q) = false, want true", ext)
			}
		})
	}

	editable := []string{
		"dng", "tif", "tiff", "jpg", "jpeg", "JPG", "heic", "heif", "psd",
		"xmp", "pp3", "nksc", "txt", "", ".",
	}
	for _, ext := range editable {
		t.Run("editable/"+ext, func(t *testing.T) {
			if IsWriteOnce(ext) {
				t.Errorf("IsWriteOnce(%q) = true, want false", ext)
			}
		})
	}
}

func TestCameraNative(t *testing.T) {
	// What a camera writes and an editor does not: the merge rule reads
	// "this group owns a master" off this table, so a container an
	// editor writes into must not answer yes.
	native := []string{
		"nef", "NEF", ".nef", "cr2", "cr3", "raf", "rw2", "arw",
		"mov", "mp4", "m4v", "avi", "mkv", "braw", "nev", "r3d",
		"mts", "m2ts", "3gp", "wmv", "asf", "mpg", "mpeg",
	}
	for _, ext := range native {
		t.Run("native/"+ext, func(t *testing.T) {
			if !CameraNative(ext) {
				t.Errorf("CameraNative(%q) = false, want true", ext)
			}
		})
	}

	// tif and dng master groups but are also what an edit is saved as,
	// so a labeled group holding one is still a derivative.
	foreign := []string{
		"tif", "tiff", "TIFF", "dng", "jpg", "jpeg", "heic", "heif",
		"psd", "xmp", "pp3", "nksc", "dop", "txt", "", ".",
	}
	for _, ext := range foreign {
		t.Run("foreign/"+ext, func(t *testing.T) {
			if CameraNative(ext) {
				t.Errorf("CameraNative(%q) = true, want false", ext)
			}
		})
	}
}

func TestIsSidecar(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a/DSC_1234.xmp", true},
		{"a/DSC_1234.XMP", true},
		{"a/DSC_1234.NEF.xmp", true},
		{"a/20170401_185236_c9e80f84.rw2.pp3", true},
		{"a/DSC_1234.nksc", true},
		{"a/NKSC_PARAM/DSC_1234.NEF.nksc", true},
		{"a/NKSC_PARAM/DSC_1234.nef.nksc", true},
		{"a/DSC_1234.nef.dop", true}, // appended shape: DxO PhotoLab
		{"a/DSC_1234.NEF", false},
		{"a/DSC_1234.jpg", false},
		{"a/A001.braw", false},
		{"a/notes.txt", false},
		{"a/NKSC_PARAM/notes.txt", false},
		{"a/DSC_1234", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsSidecar(filepath.FromSlash(tc.path)); got != tc.want {
				t.Errorf("IsSidecar(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestGroupKeyNamed(t *testing.T) {
	// One full photo group, including the sidecar that lives in a
	// different directory: a named file needs no directory logic at all.
	const prefix = "20220523_192742_d3147a94"
	members := []string{
		"2022/2022-05/20220523_192742_d3147a94.nef",
		"2022/2022-05/20220523_192742_d3147a94.xmp",
		"2022/2022-05/20220523_192742_d3147a94.nef.xmp",
		"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc",
		"2022/2022-05/20220523_192742_d3147a94-Edit.tif",
		"2022/2022-05/20220523_192742_d3147a94-Edit.tif.pp3",
		"2022/2022-05/20220523_192742_d3147a94_pr.dng.pp3",
		// an uppercase extension is not canonical, but the file is
		// plainly this group's: selecting any member selects the group
		"2022/2022-05/20220523_192742_d3147a94.NEF",
		"elsewhere/20220523_192742_d3147a94.jpg",
	}
	for _, member := range members {
		t.Run(member, func(t *testing.T) {
			if got := GroupKey(filepath.FromSlash(member)); got != prefix {
				t.Errorf("GroupKey(%q) = %q, want %q", member, got, prefix)
			}
		})
	}

	others := []string{
		"2022/2022-05/20220524_100000_aaaaaaaa.nef",
		// the prefix must be a whole token: a copy marker wedged between
		// prefix and extension is a file of its own, not a member
		"2022/2022-05/20220523_192742_d3147a94(1).fp3",
		"2022/2022-05/20130310-20130310_172613_3577e7ff.dng", // junk before the prefix
		"2022/2022-05/x20220523_192742_d3147a94.nef",
		"2022/2022-05/20220523_192742_d3147a94beef.nef", // another scheme's slice
	}
	for _, other := range others {
		t.Run("not/"+other, func(t *testing.T) {
			if got := GroupKey(filepath.FromSlash(other)); got == prefix {
				t.Errorf("GroupKey(%q) = %q, want a different group", other, got)
			}
		})
	}
}

func TestGroupKeyUnnamed(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Files not yet named group by directory and original base name.
		{"card/DSC_1234.NEF", "card/DSC_1234"},
		{"card/DSC_1234.xmp", "card/DSC_1234"},
		{"card/DSC_1234.NEF.xmp", "card/DSC_1234"},
		{"card/DSC_1235.NEF", "card/DSC_1235"},
		// Vendor sidecar subdirectory: the file belongs to the master
		// one level up (Nikon NX Studio).
		{"card/NKSC_PARAM/DSC_1234.NEF.nksc", "card/DSC_1234"},
		{"card/NKSC_PARAM/DSC_1234.nksc", "card/DSC_1234"},
		{"card/nksc_param/DSC_1234.NEF.nksc", "card/DSC_1234"},
		{"card/NKSC_PARAM/DSC_1234.NEF.NKSC", "card/DSC_1234"},
		// The same rule does not fire outside the subdirectory, and the
		// name still splits at its first dot.
		{"card/DSC_1234.NEF.nksc", "card/DSC_1234"},
		{"card/NKSC_PARAM/notes.txt", "card/NKSC_PARAM/notes"},
		// Groups are per directory: two cards, two photos.
		{"card/100ND780/DSC_1234.NEF", "card/100ND780/DSC_1234"},
		{"card/101ND780/DSC_1234.NEF", "card/101ND780/DSC_1234"},
		// A labeled derivative keys on its own base; merging it into
		// the master's group is the caller's set-level rule.
		{"card/DSC1234-Edit.tif", "card/DSC1234-Edit"},
		{"card/DSC1234.NEF", "card/DSC1234"},
		{"card/IMG.NEF", "card/IMG"},
		{"card/IMG_01.NEF", "card/IMG_01"},
		{"DSC_1234.NEF", "DSC_1234"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := GroupKey(filepath.FromSlash(tc.path))
			if want := filepath.FromSlash(tc.want); got != want {
				t.Errorf("GroupKey(%q) = %q, want %q", tc.path, got, want)
			}
		})
	}
}
