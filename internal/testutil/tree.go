package testutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtureMark introduces a Tree value that names a fixture to copy
// rather than content to write.
const fixtureMark = "@"

// Tree builds a file tree under root. Keys are paths relative to root,
// always slash-separated whatever the platform. A value of "@name"
// copies the fixture called name; any other value is the file's literal
// content. Directories are created as needed, and root itself is created
// even when the spec is empty.
//
//	testutil.Tree(t, dir, map[string]string{
//		"2026/2026-07/DSC_1234.jpg": "@dated.jpg",
//		".stampla":                  "layout = \"{yyyy}/{yyyy}-{mm}\"\n",
//	})
//
// A key that would leave root fails the test rather than writing: a
// helper that escapes the temporary directory it was given is a helper
// that eventually deletes something. Content that must itself begin with
// "@" goes through WriteFile instead.
func Tree(t *testing.T, root string, spec map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("testutil: creating %s: %v", root, err)
	}
	for name, content := range spec {
		path, err := treePath(root, name)
		if err != nil {
			t.Fatalf("testutil: Tree: %v", err)
		}
		if fixture, ok := strings.CutPrefix(content, fixtureMark); ok {
			CopyFixture(t, fixture, path)
			continue
		}
		WriteFile(t, path, []byte(content))
	}
}

// RelPaths lists every file under root as a slash-separated path
// relative to it, sorted — the shape a test states an expected archive
// in. Directories are not listed, so an empty one does not show.
func RelPaths(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("testutil: walking %s: %v", root, err)
	}
	slices.Sort(found)
	return found
}

// treePath resolves one spec key against root. It is a pure function so
// that what it refuses is table-testable.
func treePath(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("path %q is absolute", name)
	}
	// Join cleans, so a path that climbed out of root no longer relates
	// to it; Rel is what notices.
	path := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q leaves the root", name)
	}
	if rel == "." {
		return "", fmt.Errorf("path %q is the root itself", name)
	}
	return path, nil
}
