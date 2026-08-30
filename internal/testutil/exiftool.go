package testutil

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// updatedLine is what ExifTool prints for a write that landed. Its exit
// status already says as much, but a write that quietly did nothing is
// the failure this package must never pass off as a stamped file.
const updatedLine = "1 image files updated"

// RequireExifTool skips the calling test when ExifTool is not installed.
//
// The gate is deliberately no more than a PATH lookup. It does not ask
// internal/exif whether the installed ExifTool is usable, because that
// answer is itself code under test: wiring it in here would let a bug in
// it skip the very tests that would have caught the bug. An ExifTool
// that is installed but broken is not a reason to skip either — it fails
// the first helper that runs it, which is the report a developer needs.
func RequireExifTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool is not on PATH")
	}
}

// ExifTags reads a file's metadata the way stampla does: every tag, with
// family-0 group names ("EXIF:DateTimeOriginal"), plus the MD5
// image-data hash under File:ImageDataHash. SourceFile is dropped.
//
// It runs a fresh ExifTool rather than stampla's pool, so what a test
// reads back never depends on the code that wrote it.
func ExifTags(t *testing.T, path string) map[string]string {
	t.Helper()
	RequireExifTool(t)
	out := exifTool(t,
		"-j", "-a", "-G0", "-api", "imagehashtype=MD5", "-ImageDataHash", "-All",
		argPath(path))
	return decodeTags(t, path, out)
}

// ImageDataHash is the MD5 ExifTool reports for a file's payload alone —
// the half of an identity a metadata rewrite must never move. It is
// empty for a format with no payload to hash, such as a sidecar.
func ImageDataHash(t *testing.T, path string) string {
	t.Helper()
	return strings.ToLower(ExifTags(t, path)["File:ImageDataHash"])
}

// exifWrite stamps tags onto a file in place.
func exifWrite(t *testing.T, path string, tags ...string) {
	t.Helper()
	RequireExifTool(t)
	args := make([]string, 0, len(tags)+2)
	args = append(args, "-overwrite_original")
	args = append(args, tags...)
	args = append(args, argPath(path))

	if out := exifTool(t, args...); !strings.Contains(out, updatedLine) {
		t.Fatalf("testutil: exiftool did not update %s: %s", path, out)
	}
}

// exifTool runs ExifTool once and returns its standard output. ExifTool
// exits non-zero whenever a read or a write did not happen, so its
// status carries the whole verdict.
func exifTool(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("exiftool", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("testutil: exiftool %s: %v\n%s%s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// argPath keeps a leading "-" from reaching ExifTool as an option.
func argPath(path string) string {
	if strings.HasPrefix(path, "-") {
		return "./" + path
	}
	return path
}

func decodeTags(t *testing.T, path, out string) map[string]string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(out))
	// Numbers keep the text ExifTool printed, not a float's idea of it.
	decoder.UseNumber()
	var docs []map[string]any
	if err := decoder.Decode(&docs); err != nil {
		t.Fatalf("testutil: unparsable exiftool output for %s: %v\n%s", path, err, out)
	}
	if len(docs) != 1 {
		t.Fatalf("testutil: exiftool answered a one-file read for %s with %d results", path, len(docs))
	}
	tags := make(map[string]string, len(docs[0]))
	for name, value := range docs[0] {
		if name == "SourceFile" {
			continue
		}
		tags[name] = tagValue(value)
	}
	return tags
}

// tagValue flattens a JSON value to comparable text, keeping structured
// tags as their JSON so nothing is silently lost.
func tagValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}
