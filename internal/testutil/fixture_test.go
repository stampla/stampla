package testutil

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fixtureNames is every media fixture, in the order testdata/README.md
// lists them.
var fixtureNames = []string{
	"date.mp4", "date.mov", "nodate.mp4",
	"dated.jpg", "plain.jpg", "dated.xmp",
}

func TestEveryFixtureIsPresentAndNotEmpty(t *testing.T) {
	for _, name := range fixtureNames {
		if len(Fixture(t, name)) == 0 {
			t.Errorf("fixture %s is empty", name)
		}
	}
}

// The fixtures are the vocabulary every integration test speaks, so an
// unannounced addition to testdata is a change to that vocabulary.
func TestTestdataHoldsNothingUndocumented(t *testing.T) {
	want := append(slices.Clone(fixtureNames), "README.md")
	slices.Sort(want)
	if got := RelPaths(t, fixtureDir); !slices.Equal(got, want) {
		t.Errorf("testdata holds %v, want %v", got, want)
	}
}

// The metadata each fixture carries is the claim testdata/README.md
// makes and every other test relies on.
func TestFixturesCarryTheMetadataTheyClaim(t *testing.T) {
	RequireExifTool(t)
	dir := t.TempDir()

	for _, fixture := range []struct {
		name string
		hash string
		tags map[string]string
		gone []string
	}{
		{
			name: "date.mp4",
			hash: VideoHash,
			tags: map[string]string{
				"File:FileType":             "MP4",
				"QuickTime:CreateDate":      VideoDate,
				"QuickTime:TrackCreateDate": VideoDate,
			},
		},
		{
			name: "date.mov",
			hash: VideoHash,
			tags: map[string]string{
				"File:FileType":        "MOV",
				"QuickTime:CreateDate": VideoDate,
			},
		},
		{
			name: "nodate.mp4",
			hash: VideoHash,
			tags: map[string]string{
				"File:FileType": "MP4",
				// ExifTool prints an unset QuickTime date as zeroes;
				// there is no capture time here to resolve.
				"QuickTime:CreateDate": "0000:00:00 00:00:00",
			},
		},
		{
			name: "dated.jpg",
			hash: JPEGHash,
			tags: map[string]string{
				"File:FileType":         "JPEG",
				"EXIF:DateTimeOriginal": JPEGDate,
				"EXIF:CreateDate":       JPEGDate,
			},
		},
		{
			name: "plain.jpg",
			hash: JPEGHash,
			tags: map[string]string{"File:FileType": "JPEG"},
			gone: []string{"EXIF:DateTimeOriginal", "EXIF:CreateDate", "File:Comment"},
		},
		{
			name: "dated.xmp",
			// A sidecar has no payload to hash, which is not a failure.
			hash: "",
			tags: map[string]string{
				"File:FileType": "XMP",
				// ExifTool prints the sidecar's ISO value in its own form.
				"XMP:DateTimeOriginal": JPEGDate,
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(dir, fixture.name)
			CopyFixture(t, fixture.name, path)

			tags := ExifTags(t, path)
			for name, want := range fixture.tags {
				if got := tags[name]; got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range fixture.gone {
				if got, ok := tags[name]; ok {
					t.Errorf("%s = %q, want it stripped", name, got)
				}
			}
			if got := ImageDataHash(t, path); got != fixture.hash {
				t.Errorf("ImageDataHash = %q, want %q", got, fixture.hash)
			}
		})
	}
}

// The identity scheme rests on this: metadata is not part of what a file
// is. plain.jpg is dated.jpg with everything but the pixels taken off,
// so the two must differ as bytes and agree as identities.
func TestStrippedJPEGKeepsItsIdentity(t *testing.T) {
	RequireExifTool(t)
	dir := t.TempDir()
	dated := filepath.Join(dir, "dated.jpg")
	plain := filepath.Join(dir, "plain.jpg")
	CopyFixture(t, "dated.jpg", dated)
	CopyFixture(t, "plain.jpg", plain)

	if bytes.Equal(Fixture(t, "dated.jpg"), Fixture(t, "plain.jpg")) {
		t.Fatal("dated.jpg and plain.jpg are the same bytes, so the pair proves nothing")
	}
	datedHash, plainHash := ImageDataHash(t, dated), ImageDataHash(t, plain)
	if datedHash != plainHash {
		t.Errorf("ImageDataHash %s for dated.jpg but %s for plain.jpg", datedHash, plainHash)
	}
	if datedHash != JPEGHash {
		t.Errorf("ImageDataHash = %s, want the recorded %s", datedHash, JPEGHash)
	}
}

func TestFixtureIsACopy(t *testing.T) {
	first := Fixture(t, "dated.jpg")
	first[0] ^= 0xff
	if second := Fixture(t, "dated.jpg"); bytes.Equal(first, second) {
		t.Error("mutating what Fixture returned reached the committed fixture")
	}
}

func TestFixtureRejectsAnUnknownName(t *testing.T) {
	// A misspelled fixture must not read as an empty file.
	if _, err := readFixture("no-such-fixture.jpg"); err == nil {
		t.Error("reading a fixture that does not exist succeeded")
	}
}

func TestCopyFixtureCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026", "2026-07", "DSC_1234.jpg")
	CopyFixture(t, "dated.jpg", path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the copy: %v", err)
	}
	if !bytes.Equal(got, Fixture(t, "dated.jpg")) {
		t.Error("the copy does not match the fixture")
	}
}

func TestWriteFileCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "deeper", ".stampla")
	want := []byte("layout = \"{yyyy}/{yyyy}-{mm}\"\n")
	WriteFile(t, path, want)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what was written: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("wrote %q, read back %q", want, got)
	}
}
