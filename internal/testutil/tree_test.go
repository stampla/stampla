package testutil

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTreeBuildsTheShapeItWasGiven(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	Tree(t, root, map[string]string{
		".stampla":                   "layout = \"{yyyy}/{yyyy}-{mm}\"\n",
		"2026/2026-07/notes.txt":     "loose text\n",
		"2026/2026-07/deep/note.txt": "deeper\n",
		"2026/2026-08/another.txt":   "elsewhere\n",
	})

	want := []string{
		".stampla",
		"2026/2026-07/deep/note.txt",
		"2026/2026-07/notes.txt",
		"2026/2026-08/another.txt",
	}
	if got := RelPaths(t, root); !slices.Equal(got, want) {
		t.Errorf("tree holds %v, want %v", got, want)
	}

	got, err := os.ReadFile(filepath.Join(root, "2026", "2026-07", "notes.txt"))
	if err != nil {
		t.Fatalf("reading a written file: %v", err)
	}
	if string(got) != "loose text\n" {
		t.Errorf("content = %q, want the literal spec value", got)
	}
}

func TestTreeCopiesFixtures(t *testing.T) {
	root := t.TempDir()
	Tree(t, root, map[string]string{
		"cards/DSC_1234.jpg":     "@dated.jpg",
		"cards/DSC_1234.nef.xmp": "@dated.xmp",
		"cards/MVI_0001.mp4":     "@date.mp4",
		"cards/readme.txt":       "not a fixture reference\n",
	})

	want := []string{
		"cards/DSC_1234.jpg",
		"cards/DSC_1234.nef.xmp",
		"cards/MVI_0001.mp4",
		"cards/readme.txt",
	}
	if got := RelPaths(t, root); !slices.Equal(got, want) {
		t.Errorf("tree holds %v, want %v", got, want)
	}

	got, err := os.ReadFile(filepath.Join(root, "cards", "DSC_1234.jpg"))
	if err != nil {
		t.Fatalf("reading the copied fixture: %v", err)
	}
	if !bytes.Equal(got, Fixture(t, "dated.jpg")) {
		t.Error("the copy does not match dated.jpg")
	}
}

func TestTreeCreatesAnEmptyRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty", "archive")
	Tree(t, root, nil)

	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", root)
	}
	if got := RelPaths(t, root); len(got) != 0 {
		t.Errorf("an empty spec left %v behind", got)
	}
}

func TestRelPathsListsFilesOnly(t *testing.T) {
	root := t.TempDir()
	Tree(t, root, map[string]string{"a/b/file.txt": "x"})
	if err := os.MkdirAll(filepath.Join(root, "a", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	want := []string{"a/b/file.txt"}
	if got := RelPaths(t, root); !slices.Equal(got, want) {
		t.Errorf("RelPaths = %v, want %v", got, want)
	}
}

func TestTreePath(t *testing.T) {
	root := filepath.Join("archive", "root")
	for _, c := range []struct {
		given string
		want  string // "" means the path must be refused
	}{
		{"a.jpg", filepath.Join(root, "a.jpg")},
		{"2026/2026-07/a.jpg", filepath.Join(root, "2026", "2026-07", "a.jpg")},
		{"./a.jpg", filepath.Join(root, "a.jpg")},
		{"a/../b.jpg", filepath.Join(root, "b.jpg")},
		{"", ""},
		{".", ""},
		{"..", ""},
		{"../escape.jpg", ""},
		{"a/../../escape.jpg", ""},
		{"/etc/passwd", ""},
	} {
		got, err := treePath(root, c.given)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("treePath(%q) = %q, want it refused", c.given, got)
		case c.want != "" && err != nil:
			t.Errorf("treePath(%q) = %v", c.given, err)
		case c.want != "" && got != c.want:
			t.Errorf("treePath(%q) = %q, want %q", c.given, got, c.want)
		}
	}
}
