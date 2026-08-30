package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/finding"
)

// build lays out a tree in a temporary directory and returns its root.
// An entry ending in "/" is a directory; "path=content" writes content;
// anything else is an empty file. The scanner never reads file content,
// so empty files are the whole fixture.
func build(t *testing.T, entries ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, entry := range entries {
		spec, content, hasContent := strings.Cut(entry, "=")
		full := filepath.Join(root, filepath.FromSlash(spec))
		if strings.HasSuffix(spec, "/") {
			mkdirAll(t, full)
			continue
		}
		mkdirAll(t, filepath.Dir(full))
		if !hasContent {
			content = ""
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// under joins slash-separated relative paths onto a root.
func under(root string, paths ...string) []string {
	full := make([]string, len(paths))
	for i, path := range paths {
		full[i] = filepath.Join(root, filepath.FromSlash(path))
	}
	return full
}

// summary renders a scan's groups as "key: member member …", relative to
// root and slash-separated, with an implied member marked "~". It shows
// grouping, membership and order in one comparable line.
func summary(root string, scan *Scan) []string {
	lines := make([]string, 0, len(scan.Groups))
	for _, group := range scan.Groups {
		parts := make([]string, 0, len(group.Members))
		for _, member := range group.Members {
			part := show(root, member.Path)
			if member.Implied {
				part += "~"
			}
			parts = append(parts, part)
		}
		lines = append(lines, show(root, group.Key)+": "+strings.Join(parts, " "))
	}
	return lines
}

// show renders a path relative to the root when it is under it; a
// canonical group key is not a path at all and comes back as it is.
func show(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// paths lists every member of every group, implied or not.
func paths(root string, scan *Scan) []string {
	var all []string
	for _, group := range scan.Groups {
		for _, member := range group.Members {
			all = append(all, show(root, member.Path))
		}
	}
	return all
}

func collect(t *testing.T, inputs []string, opts Options) *Scan {
	t.Helper()
	scan, err := Collect(inputs, opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return scan
}

func TestCollectLiteralAndRecursed(t *testing.T) {
	root := build(t,
		"card/DSC_1234.NEF",
		"card/DSC_1235.NEF",
		"card/sub/DSC_1236.NEF",
		"other/DSC_1237.NEF",
	)

	cases := []struct {
		name   string
		inputs []string
		want   []string
	}{
		{
			// A named file is taken literally: its neighbor is not this
			// run's business, only its own group is.
			name:   "one file",
			inputs: under(root, "card/DSC_1234.NEF"),
			want:   []string{"card/DSC_1234.NEF"},
		},
		{
			name:   "two files across directories",
			inputs: under(root, "card/DSC_1235.NEF", "other/DSC_1237.NEF"),
			want:   []string{"card/DSC_1235.NEF", "other/DSC_1237.NEF"},
		},
		{
			name:   "a directory recurses",
			inputs: under(root, "card"),
			want: []string{
				"card/DSC_1234.NEF", "card/DSC_1235.NEF", "card/sub/DSC_1236.NEF",
			},
		},
		{
			name:   "the whole tree",
			inputs: []string{root},
			want: []string{
				"card/DSC_1234.NEF", "card/DSC_1235.NEF",
				"card/sub/DSC_1236.NEF", "other/DSC_1237.NEF",
			},
		},
		{
			// Overlapping inputs select each file once.
			name:   "a file inside a recursed directory",
			inputs: under(root, "card", "card/DSC_1234.NEF", "card/sub"),
			want: []string{
				"card/DSC_1234.NEF", "card/DSC_1235.NEF", "card/sub/DSC_1236.NEF",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := collect(t, tc.inputs, Options{})
			if len(scan.Errors) != 0 {
				t.Fatalf("findings = %v, want none", scan.Errors)
			}
			if got := paths(root, scan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("members = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectStdin(t *testing.T) {
	root := build(t, "card/DSC_1234.NEF", "card/DSC_1235.NEF")
	listed := under(root, "card/DSC_1234.NEF", "card/DSC_1235.NEF")
	first, second := listed[0], listed[1]

	cases := []struct {
		name   string
		list   string
		nulSep bool
		want   []string
	}{
		{
			name: "newline delimited",
			list: first + "\n" + second + "\n",
			want: []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			// A list may end without its terminator.
			name: "no trailing newline",
			list: first + "\n" + second,
			want: []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			// A list produced on Windows names the same files.
			name: "crlf line endings",
			list: first + "\r\n" + second + "\r\n",
			want: []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			name: "blank lines are not paths",
			list: "\n" + first + "\n\n" + second + "\n\n",
			want: []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			name:   "nul delimited",
			list:   first + "\x00" + second + "\x00",
			nulSep: true,
			want:   []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			name:   "nul delimited without a terminator",
			list:   first + "\x00" + second,
			nulSep: true,
			want:   []string{"card/DSC_1234.NEF", "card/DSC_1235.NEF"},
		},
		{
			name: "an empty list selects nothing",
			list: "",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The positional argument is ignored with --stdin; the list
			// replaces it, so this nonexistent path must not be a finding.
			scan := collect(t, []string{filepath.Join(root, "ignored")}, Options{
				Stdin:  strings.NewReader(tc.list),
				NulSep: tc.nulSep,
			})
			if len(scan.Errors) != 0 {
				t.Fatalf("findings = %v, want none", scan.Errors)
			}
			if got := paths(root, scan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("members = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectStdinReadError(t *testing.T) {
	// A list that cannot be read is not a per-path finding: the run does
	// not know what it was asked to do.
	if _, err := Collect(nil, Options{Stdin: failingReader{}}); err == nil {
		t.Fatal("Collect with an unreadable list = nil error, want one")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestCollectUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny listing on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	modes := []struct {
		name string
		perm os.FileMode
	}{
		// Traversable but not listable: the marker reads as absent, so
		// only the walk's own error callback can report this directory.
		// Suppressing it is exactly how a scan comes to call an
		// unreadable card safe to format.
		{name: "listing denied", perm: 0o111},
		// Nothing readable at all, marker included.
		{name: "closed entirely", perm: 0o000},
	}

	for _, mode := range modes {
		for _, stopAtRoots := range []bool{false, true} {
			verb := map[bool]string{false: "verify", true: "mutation"}[stopAtRoots]
			t.Run(mode.name+"/"+verb, func(t *testing.T) {
				// The unreadable directory is nowhere near the selected
				// file's group, so nothing but the walk itself looks at
				// it: only the walk's error callback can report it.
				root := build(t, "card/DSC_1234.NEF", "deep/locked/DSC_9999.NEF")
				locked := filepath.Join(root, "deep", "locked")
				if err := os.Chmod(locked, mode.perm); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

				scan := collect(t, []string{root}, Options{StopAtRoots: stopAtRoots})
				// The readable part of the tree still scans.
				if got, want := paths(root, scan), []string{"card/DSC_1234.NEF"}; !reflect.DeepEqual(got, want) {
					t.Errorf("members = %v, want %v", got, want)
				}
				// The unreadable part is news, never a silence: this is
				// the scan saying it cannot account for what is in there.
				if len(scan.Errors) != 1 {
					t.Fatalf("findings = %v, want exactly one", scan.Errors)
				}
				got := scan.Errors[0]
				if got.Class != finding.Missing || got.Path != locked {
					t.Errorf("finding = %+v, want class %q on %q", got, finding.Missing, locked)
				}
				if !strings.Contains(got.Detail, "cannot scan") {
					t.Errorf("finding detail = %q, want it to say what went wrong", got.Detail)
				}
			})
		}
	}
}

func TestCollectUnreadableSidecarDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny listing on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	// The group's own neighborhood is unreadable: whether a sidecar is
	// missing cannot be told, and that is news too.
	root := build(t, "card/DSC_1234.NEF", "card/NKSC_PARAM/DSC_1234.NEF.nksc")
	vendor := filepath.Join(root, "card", "NKSC_PARAM")
	if err := os.Chmod(vendor, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(vendor, 0o755) })

	scan := collect(t, under(root, "card/DSC_1234.NEF"), Options{})
	if got, want := paths(root, scan), []string{"card/DSC_1234.NEF"}; !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
	if len(scan.Errors) != 1 || scan.Errors[0].Path != vendor {
		t.Fatalf("findings = %v, want one on %q", scan.Errors, vendor)
	}
}

func TestCollectSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := build(t, "store/DSC_1234.NEF", "card/DSC_1111.NEF")
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A link to a photo is followed once, so a symlinked file is not lost.
	link("store/DSC_1234.NEF", "card/DSC_2222.NEF")
	// A link to a directory is not descended: WalkDir does not follow
	// links, and a loop would never end.
	link("store", "card/DSC_3333.NEF")
	// A link to nothing, wearing a photo's name, is a gap to report.
	link("store/gone.NEF", "card/DSC_4444.NEF")

	scan := collect(t, under(root, "card"), Options{})
	want := []string{"card/DSC_1111.NEF", "card/DSC_2222.NEF"}
	if got := paths(root, scan); !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
	if got, want := scan.Skipped.Other, 1; got != want {
		t.Errorf("Skipped.Other = %d, want %d", got, want)
	}
	broken := filepath.Join(root, "card", "DSC_4444.NEF")
	if len(scan.Errors) != 1 || scan.Errors[0].Path != broken {
		t.Fatalf("findings = %v, want one on %q", scan.Errors, broken)
	}
}

func TestCollectVanishedMember(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	// A member that will not stat is a finding, not a quietly shorter
	// group: a group either fully converges or is reported.
	root := build(t, "card/DSC_1234.NEF")
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "card", "DSC_1234.xmp")); err != nil {
		t.Fatal(err)
	}

	scan := collect(t, under(root, "card/DSC_1234.NEF"), Options{})
	if got, want := paths(root, scan), []string{"card/DSC_1234.NEF"}; !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
	sidecar := filepath.Join(root, "card", "DSC_1234.xmp")
	if len(scan.Errors) != 1 || scan.Errors[0].Path != sidecar {
		t.Fatalf("findings = %v, want one on %q", scan.Errors, sidecar)
	}
}

func TestCollectNestedRoots(t *testing.T) {
	const layoutMarker = "layout = \"{yyyy}/{yyyy}-{mm}\"\n"
	const containerMarker = "layout-for-children = \"{yyyy}\"\n"

	cases := []struct {
		name        string
		entries     []string
		stopAtRoots bool
		want        []string
		wantRoots   []string
	}{
		{
			name: "a mutation stops at a nested root",
			entries: []string{
				"DSC_1234.NEF",
				"inner/.stampla=" + layoutMarker,
				"inner/DSC_5678.NEF",
			},
			stopAtRoots: true,
			want:        []string{"DSC_1234.NEF"},
			wantRoots:   []string{"inner"},
		},
		{
			name: "verify records the root and descends",
			entries: []string{
				"DSC_1234.NEF",
				"inner/.stampla=" + layoutMarker,
				"inner/DSC_5678.NEF",
			},
			want:      []string{"DSC_1234.NEF", "inner/DSC_5678.NEF"},
			wantRoots: []string{"inner"},
		},
		{
			// The input root's own marker does not stop its own scan.
			name: "the input root itself is not nested",
			entries: []string{
				".stampla=" + layoutMarker,
				"DSC_1234.NEF",
			},
			stopAtRoots: true,
			want:        []string{"DSC_1234.NEF"},
		},
		{
			// A container declares its children's layout, not its own; it
			// is not an archive and does not stop anything.
			name: "a container is not a root",
			entries: []string{
				"inner/.stampla=" + containerMarker,
				"inner/DSC_5678.NEF",
			},
			stopAtRoots: true,
			want:        []string{"inner/DSC_5678.NEF"},
		},
		{
			name: "nested roots at several depths",
			entries: []string{
				"a/.stampla=" + layoutMarker,
				"a/DSC_1111.NEF",
				"b/c/.stampla=" + layoutMarker,
				"b/c/DSC_2222.NEF",
				"b/DSC_3333.NEF",
			},
			stopAtRoots: true,
			want:        []string{"b/DSC_3333.NEF"},
			wantRoots:   []string{"a", "b/c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := build(t, tc.entries...)
			scan := collect(t, []string{root}, Options{StopAtRoots: tc.stopAtRoots})
			if len(scan.Errors) != 0 {
				t.Fatalf("findings = %v, want none", scan.Errors)
			}
			if got := paths(root, scan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("members = %v, want %v", got, tc.want)
			}
			var roots []string
			for _, dir := range scan.NestedRoots {
				roots = append(roots, show(root, dir))
			}
			if !reflect.DeepEqual(roots, tc.wantRoots) {
				t.Errorf("nested roots = %v, want %v", roots, tc.wantRoots)
			}
		})
	}
}

func TestCollectGroupsStopAtNestedRoots(t *testing.T) {
	// Stopping at a nested root would mean little if group expansion
	// reached into it anyway: a named group's members can live in any
	// directory, and this one lives in another archive.
	entries := []string{
		"20220523_192742_d3147a94.nef",
		"inner/.stampla=layout = \"\"\n",
		"inner/20220523_192742_d3147a94.xmp",
	}
	const key = "20220523_192742_d3147a94: "

	cases := []struct {
		name        string
		stopAtRoots bool
		want        []string
	}{
		{
			name:        "a mutation leaves the other archive alone",
			stopAtRoots: true,
			want:        []string{key + "20220523_192742_d3147a94.nef"},
		},
		{
			name: "verify sees the whole tree",
			want: []string{key + "20220523_192742_d3147a94.nef inner/20220523_192742_d3147a94.xmp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := build(t, entries...)
			scan := collect(t, []string{root}, Options{StopAtRoots: tc.stopAtRoots})
			if got := summary(root, scan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectSkipsHidden(t *testing.T) {
	root := build(t,
		"card/DSC_1234.NEF",
		"card/.DSC_9999.NEF",      // a dotfile, whatever its extension
		"card/._DSC_1234.NEF",     // an AppleDouble companion
		"card/.stampla=# nothing", // the marker is read, never converged
		"card/.cache/DSC_8888.NEF",
		"card/.cache/DSC_8887.NEF",
		"card/notes.txt",
		"card/Thumbs.db",
	)

	scan := collect(t, []string{root}, Options{})
	if len(scan.Errors) != 0 {
		t.Fatalf("findings = %v, want none", scan.Errors)
	}
	if got, want := paths(root, scan), []string{"card/DSC_1234.NEF"}; !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
	// Three dotfiles plus the dot-directory, which counts once and is
	// never descended into.
	if got, want := scan.Skipped.Hidden, 4; got != want {
		t.Errorf("Skipped.Hidden = %d, want %d", got, want)
	}
	if got, want := scan.Skipped.Other, 2; got != want {
		t.Errorf("Skipped.Other = %d, want %d", got, want)
	}
}

func TestCollectExplicitInputs(t *testing.T) {
	root := build(t, "card/DSC_1234.NEF", "card/notes.txt", "card/DSC_1234.xmp")

	cases := []struct {
		name       string
		input      string
		wantClass  finding.Class
		wantDetail string
	}{
		{
			// Never a silent skip: the user named this file.
			name:       "not media",
			input:      "card/notes.txt",
			wantClass:  finding.Unresolvable,
			wantDetail: "neither a photo",
		},
		{
			name:       "does not exist",
			input:      "card/DSC_4321.NEF",
			wantClass:  finding.Missing,
			wantDetail: "cannot scan",
		},
		{
			name:       "a directory that does not exist",
			input:      "cards",
			wantClass:  finding.Missing,
			wantDetail: "cannot scan",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(tc.input))
			scan := collect(t, []string{path}, Options{})
			if len(scan.Groups) != 0 {
				t.Errorf("groups = %v, want none", summary(root, scan))
			}
			if len(scan.Errors) != 1 {
				t.Fatalf("findings = %v, want exactly one", scan.Errors)
			}
			got := scan.Errors[0]
			if got.Class != tc.wantClass || got.Path != path {
				t.Errorf("finding = %+v, want class %q on %q", got, tc.wantClass, path)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("finding detail = %q, want it to mention %q", got.Detail, tc.wantDetail)
			}
		})
	}

	t.Run("a sidecar selects its group", func(t *testing.T) {
		// Selecting any member selects the group, so an explicit sidecar
		// is a legitimate selection, not a stray file.
		scan := collect(t, under(root, "card/DSC_1234.xmp"), Options{})
		if len(scan.Errors) != 0 {
			t.Fatalf("findings = %v, want none", scan.Errors)
		}
		want := []string{"card/DSC_1234: card/DSC_1234.NEF~ card/DSC_1234.xmp"}
		if got := summary(root, scan); !reflect.DeepEqual(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})

	t.Run("an empty path is a finding", func(t *testing.T) {
		scan := collect(t, []string{""}, Options{})
		if len(scan.Errors) != 1 || scan.Errors[0].Class != finding.Missing {
			t.Fatalf("findings = %v, want one missing finding", scan.Errors)
		}
		if len(scan.Groups) != 0 {
			t.Errorf("groups = %v, want none", summary(root, scan))
		}
	})
}

func TestCollectDeterministic(t *testing.T) {
	// Same input state, same plan: two collections of one tree must be
	// indistinguishable, groups, order, counts and findings alike.
	root := build(t,
		"card/DSC_1234.NEF",
		"card/DSC_1234.xmp",
		"card/DSC_1234.NEF.xmp",
		"card/NKSC_PARAM/DSC_1234.NEF.nksc",
		"card/DSC_1235.NEF",
		"card/DSC_1235-Edit.tif",
		"card/IMG_0001.JPG",
		"card/notes.txt",
		"card/.hidden",
		"2022/2022-05/20220523_192742_d3147a94.nef",
		"2022/2022-05/20220523_192742_d3147a94-Edit.tif",
		"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc",
		"2022/.stampla=layout = \"{yyyy}/{yyyy}-{mm}\"\n",
	)

	inputs := under(root, "card/DSC_1234.NEF", "2022", "card")
	first := collect(t, inputs, Options{})
	second := collect(t, inputs, Options{})
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two collections differ:\n%v\n%v", summary(root, first), summary(root, second))
	}
	if len(first.Groups) == 0 {
		t.Fatal("no groups collected; the fixture is not exercising anything")
	}
}

func TestItemSizes(t *testing.T) {
	// Size and ModTime come off the walk, for both selected and implied
	// members; the engine plans copies with them.
	root := build(t, "card/DSC_1234.NEF=raw bytes", "card/DSC_1234.xmp=<x:xmpmeta/>")
	scan := collect(t, under(root, "card/DSC_1234.NEF"), Options{})
	if len(scan.Groups) != 1 || len(scan.Groups[0].Members) != 2 {
		t.Fatalf("groups = %v, want one group of two", summary(root, scan))
	}
	for _, member := range scan.Groups[0].Members {
		info, err := os.Stat(member.Path)
		if err != nil {
			t.Fatal(err)
		}
		if member.Size != info.Size() {
			t.Errorf("%s: Size = %d, want %d", show(root, member.Path), member.Size, info.Size())
		}
		if !member.ModTime.Equal(info.ModTime()) {
			t.Errorf("%s: ModTime = %v, want %v", show(root, member.Path), member.ModTime, info.ModTime())
		}
	}
}
