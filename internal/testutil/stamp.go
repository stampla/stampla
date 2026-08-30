package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The two date forms a capture time is written in: exifDateLayout is
// what ExifTool prints and accepts, xmpDateLayout is what an XMP sidecar
// stores.
const (
	exifDateLayout = "2006:01:02 15:04:05"
	xmpDateLayout  = "2006-01-02T15:04:05"
)

// StampJPEG puts a JPEG at path carrying the given capture time, in
// ExifTool's date form ("2026:07:03 15:07:27"). Parent directories are
// created; an existing file is replaced.
//
// An empty time asks for plain.jpg instead — the same pixels with no
// metadata at all, the fixture for a file whose capture time cannot be
// resolved — and needs no ExifTool.
//
// The pixels are never touched, so every JPEG this writes shares
// JPEGHash and differs only in the capture-time half of its identity.
// That is how a test gets twenty distinct capture times without twenty
// binary fixtures.
func StampJPEG(t *testing.T, path, dateTimeOriginal string) {
	t.Helper()
	if dateTimeOriginal == "" {
		CopyFixture(t, "plain.jpg", path)
		return
	}
	CopyFixture(t, "dated.jpg", path)
	// The value is handed to ExifTool as given rather than reformatted,
	// so a test may write any form ExifTool accepts — a zone suffix
	// included, which stampla is required to ignore. ExifTool refuses
	// what it cannot parse, and exifWrite fails on that refusal.
	exifWrite(t, path,
		"-EXIF:DateTimeOriginal="+dateTimeOriginal,
		"-EXIF:CreateDate="+dateTimeOriginal)
}

// StampVideo puts an MP4 at path carrying the given QuickTime creation
// time, in ExifTool's date form. An empty time asks for nodate.mp4, the
// same clip with no creation date, and needs no ExifTool.
//
// Only QuickTime:CreateDate is rewritten. The clip's other QuickTime
// times keep VideoDate, so a test can always tell the tag it set from
// the ones it did not — which is what a capture-time ranking is about.
// All of them share VideoHash.
func StampVideo(t *testing.T, path, createDate string) {
	t.Helper()
	if createDate == "" {
		CopyFixture(t, "nodate.mp4", path)
		return
	}
	CopyFixture(t, "date.mp4", path)
	exifWrite(t, path, "-QuickTime:CreateDate="+createDate)
}

// WriteSidecar writes an XMP sidecar at path carrying the given capture
// time, in either date form. The text is the committed dated.xmp with
// its date substituted, so a sidecar a test gets and the sidecar in
// testdata can never drift apart. No ExifTool is involved: a sidecar is
// text, and writing one must not depend on a tool being installed.
func WriteSidecar(t *testing.T, path, dateTimeOriginal string) {
	t.Helper()
	when, err := xmpDate(dateTimeOriginal)
	if err != nil {
		t.Fatalf("testutil: WriteSidecar: %v", err)
	}
	text := string(Fixture(t, "dated.xmp"))
	if strings.Count(text, SidecarDate) != 1 {
		t.Fatalf("testutil: dated.xmp no longer holds exactly one %s to substitute", SidecarDate)
	}
	WriteFile(t, path, []byte(strings.Replace(text, SidecarDate, when, 1)))
}

// xmpDate restates a capture time in the ISO form a sidecar stores,
// accepting either form so that one date argument reads the same
// whichever helper it is given to. It is a pure function so the forms it
// accepts are table-testable.
func xmpDate(value string) (string, error) {
	for _, layout := range []string{exifDateLayout, xmpDateLayout} {
		if when, err := time.Parse(layout, value); err == nil {
			return when.Format(xmpDateLayout), nil
		}
	}
	return "", fmt.Errorf("%q is not a capture time; want %q or %q form",
		value, exifDateLayout, xmpDateLayout)
}
