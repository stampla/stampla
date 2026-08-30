package testutil

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExifTagsComeBackGroupQualified(t *testing.T) {
	RequireExifTool(t)
	path := filepath.Join(t.TempDir(), "DSC_1234.jpg")
	CopyFixture(t, "dated.jpg", path)

	tags := ExifTags(t, path)
	if len(tags) == 0 {
		t.Fatal("ExifTags returned nothing")
	}
	// Only the group name tells a maker-notes time from a QuickTime one,
	// so an unqualified key would make a ranking test meaningless.
	for name := range tags {
		if !strings.Contains(name, ":") {
			t.Errorf("tag %q came back without its group", name)
		}
	}
	if _, ok := tags["SourceFile"]; ok {
		t.Error("SourceFile is in the tag map")
	}
	if got := tags["EXIF:DateTimeOriginal"]; got != JPEGDate {
		t.Errorf("EXIF:DateTimeOriginal = %q, want %q", got, JPEGDate)
	}
}

func TestImageDataHashIsLowercaseHex(t *testing.T) {
	RequireExifTool(t)
	path := filepath.Join(t.TempDir(), "DSC_1234.jpg")
	CopyFixture(t, "dated.jpg", path)

	got := ImageDataHash(t, path)
	if len(got) != 32 || strings.ToLower(got) != got {
		t.Errorf("ImageDataHash = %q, want 32 lowercase hex digits", got)
	}
	if got != JPEGHash {
		t.Errorf("ImageDataHash = %q, want %q", got, JPEGHash)
	}
}

// A path ExifTool would read as an option must still name a file.
func TestExifTagsReadsAPathThatLooksLikeAnOption(t *testing.T) {
	RequireExifTool(t)
	dir := t.TempDir()
	CopyFixture(t, "dated.jpg", filepath.Join(dir, "-overwrite_original.jpg"))
	t.Chdir(dir)

	if got := ExifTags(t, "-overwrite_original.jpg")["EXIF:DateTimeOriginal"]; got != JPEGDate {
		t.Errorf("EXIF:DateTimeOriginal = %q, want %q", got, JPEGDate)
	}
}

// The fixture directory is found from this package's source path, so a
// test that has moved elsewhere still reads it.
func TestFixturesAreReadableFromAnotherDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	if len(Fixture(t, "dated.jpg")) == 0 {
		t.Error("no fixture content after changing directory")
	}
}

func TestArgPath(t *testing.T) {
	for given, want := range map[string]string{
		"photo.jpg":     "photo.jpg",
		"-dashed.jpg":   "./-dashed.jpg",
		"/tmp/abs.jpg":  "/tmp/abs.jpg",
		"dir/photo.jpg": "dir/photo.jpg",
	} {
		if got := argPath(given); got != want {
			t.Errorf("argPath(%q) = %q, want %q", given, got, want)
		}
	}
}
