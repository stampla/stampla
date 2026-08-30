package testutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The point of the stamping helpers: distinct capture times, one payload,
// no new binary fixtures.
func TestStampJPEGGivesEachCopyItsOwnCaptureTime(t *testing.T) {
	RequireExifTool(t)
	dir := t.TempDir()
	dates := []string{
		"2019:11:05 08:09:10",
		"2026:07:03 15:07:27",
		"2001:02:03 04:05:06",
	}

	for i, want := range dates {
		// A subdirectory per file also proves the parents are created.
		path := filepath.Join(dir, fmt.Sprintf("day%d", i), "DSC_1234.jpg")
		StampJPEG(t, path, want)

		tags := ExifTags(t, path)
		for _, name := range []string{"EXIF:DateTimeOriginal", "EXIF:CreateDate"} {
			if got := tags[name]; got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := ImageDataHash(t, path); got != JPEGHash {
			t.Errorf("ImageDataHash = %q, want the untouched payload %q", got, JPEGHash)
		}
	}
}

func TestStampJPEGWithNoDateIsThePlainFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unresolvable.jpg")
	StampJPEG(t, path, "")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the stamped file: %v", err)
	}
	if !bytes.Equal(got, Fixture(t, "plain.jpg")) {
		t.Error("StampJPEG with no date did not write plain.jpg")
	}
}

func TestStampJPEGWithNoDateCarriesNoCaptureTime(t *testing.T) {
	RequireExifTool(t)
	path := filepath.Join(t.TempDir(), "unresolvable.jpg")
	StampJPEG(t, path, "")

	tags := ExifTags(t, path)
	for _, name := range []string{"EXIF:DateTimeOriginal", "EXIF:CreateDate"} {
		if got, ok := tags[name]; ok {
			t.Errorf("%s = %q, want no capture time at all", name, got)
		}
	}
}

func TestStampVideoGivesEachCopyItsOwnCreationTime(t *testing.T) {
	RequireExifTool(t)
	dir := t.TempDir()
	dates := []string{"2019:11:05 08:09:10", "2026:07:03 13:07:27"}

	for i, want := range dates {
		path := filepath.Join(dir, fmt.Sprintf("clip%d", i), "MVI_0001.mp4")
		StampVideo(t, path, want)

		tags := ExifTags(t, path)
		if got := tags["QuickTime:CreateDate"]; got != want {
			t.Errorf("QuickTime:CreateDate = %q, want %q", got, want)
		}
		// Only CreateDate moves, so a ranking test can tell the tag that
		// was set from the ones that were not.
		if got := tags["QuickTime:TrackCreateDate"]; got != VideoDate {
			t.Errorf("QuickTime:TrackCreateDate = %q, want the fixture's own %q", got, VideoDate)
		}
		if got := ImageDataHash(t, path); got != VideoHash {
			t.Errorf("ImageDataHash = %q, want the untouched payload %q", got, VideoHash)
		}
	}
}

func TestStampVideoWithNoDateIsTheDatelessFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MVI_0002.mp4")
	StampVideo(t, path, "")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the stamped file: %v", err)
	}
	if !bytes.Equal(got, Fixture(t, "nodate.mp4")) {
		t.Error("StampVideo with no date did not write nodate.mp4")
	}
}

func TestWriteSidecarSubstitutesTheDate(t *testing.T) {
	dir := t.TempDir()
	// Either date form names the same wall-clock time and must produce
	// the same sidecar.
	for _, given := range []string{"2019:11:05 08:09:10", "2019-11-05T08:09:10"} {
		path := filepath.Join(dir, "sidecars", "DSC_1234.nef.xmp")
		WriteSidecar(t, path, given)

		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the sidecar: %v", err)
		}
		if want := "2019-11-05T08:09:10"; !strings.Contains(string(text), want) {
			t.Errorf("sidecar for %q does not carry %s:\n%s", given, want, text)
		}
		if strings.Contains(string(text), SidecarDate) {
			t.Errorf("sidecar for %q still carries the fixture's own date", given)
		}
	}
}

func TestWriteSidecarReadsBackThroughExifTool(t *testing.T) {
	RequireExifTool(t)
	path := filepath.Join(t.TempDir(), "DSC_1234.nef.xmp")
	WriteSidecar(t, path, "2019:11:05 08:09:10")

	tags := ExifTags(t, path)
	if got, want := tags["XMP:DateTimeOriginal"], "2019:11:05 08:09:10"; got != want {
		t.Errorf("XMP:DateTimeOriginal = %q, want %q", got, want)
	}
	if got := tags["File:FileType"]; got != "XMP" {
		t.Errorf("File:FileType = %q, want XMP", got)
	}
	// A sidecar has no payload, and that is not a failure.
	if got := ImageDataHash(t, path); got != "" {
		t.Errorf("ImageDataHash = %q, want none for a sidecar", got)
	}
}

func TestXMPDate(t *testing.T) {
	for _, c := range []struct {
		given string
		want  string
	}{
		{"2026:07:03 15:07:27", "2026-07-03T15:07:27"},
		{"2026-07-03T15:07:27", "2026-07-03T15:07:27"},
		{"1999:12:31 23:59:59", "1999-12-31T23:59:59"},
		{"", ""},
		{"2026:07:03", ""},
		{"03/07/2026 15:07:27", ""},
		{"2026:07:03 15:07:27+02:00", ""},
		{"2026:13:03 15:07:27", ""},
	} {
		got, err := xmpDate(c.given)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("xmpDate(%q) = %q, want an error", c.given, got)
		case c.want != "" && err != nil:
			t.Errorf("xmpDate(%q) = %v", c.given, err)
		case got != c.want && c.want != "":
			t.Errorf("xmpDate(%q) = %q, want %q", c.given, got, c.want)
		}
	}
}
