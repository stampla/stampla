package exif

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReadArgsAlwaysRequestGroupsAndTheHash(t *testing.T) {
	for _, tags := range [][]string{nil, {"DateTimeOriginal"}} {
		args := readArgs(tags)
		for _, want := range []string{"-j", "-a", "-G0", "-ImageDataHash"} {
			if !slices.Contains(args, want) {
				t.Errorf("readArgs(%q) = %q, missing %s", tags, args, want)
			}
		}
		if i := slices.Index(args, "-api"); i < 0 || args[i+1] != "imagehashtype=MD5" {
			t.Errorf("readArgs(%q) = %q, imagehashtype is not an -api setting", tags, args)
		}
		// Without these on the list, a file ExifTool cannot parse
		// comes back as an empty result rather than a failure.
		for _, want := range []string{"-Error", "-Warning"} {
			if !slices.Contains(args, want) {
				t.Errorf("readArgs(%q) = %q, missing %s", tags, args, want)
			}
		}
	}
}

func TestReadArgsCarryTheRequestedTags(t *testing.T) {
	args := readArgs([]string{"DateTimeOriginal", "EXIF:CreateDate", "XMP:all"})
	for _, want := range []string{"-DateTimeOriginal", "-EXIF:CreateDate", "-XMP:all"} {
		if !slices.Contains(args, want) {
			t.Errorf("readArgs = %q, missing %s", args, want)
		}
	}
	// A whole-file dump is what an explicit list exists to avoid.
	if slices.Contains(args, "-All") {
		t.Errorf("readArgs = %q, asks for every tag anyway", args)
	}
	// The documented fallback.
	for _, empty := range [][]string{nil, {}} {
		if !slices.Contains(readArgs(empty), "-All") {
			t.Errorf("readArgs(%q) does not fall back to -All", empty)
		}
	}
}

func TestCheckTag(t *testing.T) {
	for _, tag := range []string{"DateTimeOriginal", "EXIF:CreateDate", "XMP:all", "ImageDataHash"} {
		if err := checkTag(tag); err != nil {
			t.Errorf("checkTag(%q) = %v, want nil", tag, err)
		}
	}
	// A name reaches ExifTool as "-Name", so one of these would
	// arrive as a write or as arguments of its own.
	for _, tag := range []string{
		"", "DateTimeOriginal=2001:01:01 00:00:00", "-overwrite_original",
		"Artist\n-overwrite_original", "Artist\rMake",
	} {
		if err := checkTag(tag); !errors.Is(err, ErrBadTag) {
			t.Errorf("checkTag(%q) = %v, want ErrBadTag", tag, err)
		}
	}
}

func TestReadArgsCarryNoWriteOption(t *testing.T) {
	forbidden := []string{
		"-overwrite_original", "-overwrite_original_in_place", "-delete_original",
		"-restore_original", "-tagsfromfile", "-geotag", "-b",
	}
	for _, tags := range [][]string{nil, {"DateTimeOriginal", "EXIF:Artist"}} {
		for _, arg := range readArgs(tags) {
			if slices.Contains(forbidden, arg) {
				t.Errorf("readArgs(%q) contains the write-capable argument %q", tags, arg)
			}
			// A tag assignment is "-Tag=value"; the -api settings are
			// not options and carry no dash.
			if strings.HasPrefix(arg, "-") && strings.Contains(arg, "=") {
				t.Errorf("readArgs(%q) contains a tag assignment %q", tags, arg)
			}
		}
	}
	// Each read must build its own argument list; a shared one would
	// let a chunk's file names leak into the next chunk's command.
	first, second := readArgs(nil), readArgs(nil)
	first = append(first, "leaked.jpg")
	if slices.Contains(second, "leaked.jpg") || len(second) != len(first)-1 {
		t.Error("readArgs hands out a shared slice")
	}
}

func TestPayloadIsOneArgumentPerLine(t *testing.T) {
	body, err := payload([]string{"-j", "-a", "photo.jpg"}, 7)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	want := "-echo4\n{ready7}\n-j\n-a\nphoto.jpg\n-execute7\n"
	if got := string(body); got != want {
		t.Errorf("payload =\n%q\nwant\n%q", got, want)
	}
}

func TestPayloadRefusesNewlines(t *testing.T) {
	for _, arg := range []string{
		"photo\n-delete_original.jpg",
		"photo\r-delete_original.jpg",
		"trailing.jpg\n",
	} {
		if _, err := payload([]string{"-j", arg}, 1); !errors.Is(err, ErrNewlineInPath) {
			t.Errorf("payload(%q) error = %v, want ErrNewlineInPath", arg, err)
		}
	}
}

func TestCheckPath(t *testing.T) {
	tests := []struct {
		path string
		want error
	}{
		{"photo.jpg", nil},
		{"/archive/2026/photo.jpg", nil},
		{"-starts-with-a-dash.jpg", nil},
		{"space and 'quote'.jpg", nil},
		{"", ErrEmptyPath},
		{"a\nb.jpg", ErrNewlineInPath},
		{"a\rb.jpg", ErrNewlineInPath},
		{"a\r\n-overwrite_original\nb.jpg", ErrNewlineInPath},
	}
	for _, tt := range tests {
		if err := checkPath(tt.path); !errors.Is(err, tt.want) {
			t.Errorf("checkPath(%q) = %v, want %v", tt.path, err, tt.want)
		}
	}
}

func TestProtocolPathQuotesALeadingDash(t *testing.T) {
	// ExifTool reads "-dash.jpg" as an option, so a path that looks
	// like one must be handed over as an unmistakable file name.
	if got := protocolPath("-dash.jpg"); got != "./-dash.jpg" {
		t.Errorf("protocolPath(-dash.jpg) = %q, want ./-dash.jpg", got)
	}
	if got := protocolPath("photo.jpg"); got != "photo.jpg" {
		t.Errorf("protocolPath(photo.jpg) = %q, want photo.jpg", got)
	}
	if normalize(protocolPath("-dash.jpg")) != normalize("-dash.jpg") {
		t.Error("quoting a leading dash changed the path's identity")
	}
}

func TestNormalizeMatchesWhatExifToolEchoes(t *testing.T) {
	// ExifTool answers with forward slashes on every platform.
	tests := []struct{ in, want string }{
		{"photo.jpg", "photo.jpg"},
		{"./photo.jpg", "photo.jpg"},
		{"a/b/../photo.jpg", "a/photo.jpg"},
		{"/archive/2026/photo.jpg", "/archive/2026/photo.jpg"},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := normalize(filepath.Join("a", "b", "photo.jpg")); got != "a/b/photo.jpg" {
		t.Errorf("normalize of a native path = %q, want a/b/photo.jpg", got)
	}
}

func TestShard(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		workers int
		want    [][2]int
	}{
		{"nothing", 0, 4, nil},
		{"one file one worker", 1, 1, [][2]int{{0, 1}}},
		{"a small batch reaches every worker", 8, 4, [][2]int{{0, 2}, {2, 4}, {4, 6}, {6, 8}}},
		{"fewer files than workers", 3, 8, [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{"an uneven batch leaves a short chunk", 7, 3, [][2]int{{0, 3}, {3, 6}, {6, 7}}},
		{"a big batch is capped at the chunk size", 2001, 2, [][2]int{
			{0, 500}, {500, 1000}, {1000, 1500}, {1500, 2000}, {2000, 2001},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shard(tt.n, tt.workers)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("shard(%d, %d) = %v, want %v", tt.n, tt.workers, got, tt.want)
			}
			covered := 0
			for _, span := range got {
				covered += span[1] - span[0]
				if span[1]-span[0] > chunkSize {
					t.Errorf("chunk %v exceeds the cap of %d", span, chunkSize)
				}
			}
			if covered != tt.n {
				t.Errorf("chunks cover %d of %d paths", covered, tt.n)
			}
		})
	}
}

// A trimmed but otherwise verbatim answer from exiftool 13.55.
const cannedAnswer = `[{
  "SourceFile": "/archive/DSC_1234.NEF",
  "File:ImageDataHash": "F327C55CB82F5877303559AF3F67DCD6",
  "ExifTool:ExifToolVersion": 13.55,
  "File:FileName": "DSC_1234.NEF",
  "File:FileType": "NEF",
  "EXIF:Make": "NIKON CORPORATION",
  "EXIF:DateTimeOriginal": "2026:07:03 15:07:27",
  "MakerNotes:TimeZone": "+02:00",
  "XMP:Subject": ["holiday","boat"],
  "Composite:Megapixels": 45.4,
  "ExifTool:Warning": "Bad MakerNotes directory"
},
{
  "SourceFile": "./clip.mov",
  "QuickTime:CreateDate": "2026:07:03 13:07:27",
  "QuickTime:Duration": 12
},
{
  "SourceFile": "/archive/empty.jpg",
  "ExifTool:Error": "File is empty"
}]
`

func TestParseEntries(t *testing.T) {
	entries, err := parseEntries(cannedAnswer)
	if err != nil {
		t.Fatalf("parseEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("parsed %d entries, want 3", len(entries))
	}

	raw := entries[0]
	if raw.key != "/archive/DSC_1234.NEF" {
		t.Errorf("key = %q", raw.key)
	}
	if _, ok := raw.tags["SourceFile"]; ok {
		t.Error("SourceFile survived into the tag map; it is the path, not a tag")
	}
	if raw.hash != "f327c55cb82f5877303559af3f67dcd6" {
		t.Errorf("hash = %q, want it lowercased", raw.hash)
	}
	want := map[string]string{
		"EXIF:DateTimeOriginal":    "2026:07:03 15:07:27",
		"MakerNotes:TimeZone":      "+02:00",
		"EXIF:Make":                "NIKON CORPORATION",
		"ExifTool:ExifToolVersion": "13.55",
		"Composite:Megapixels":     "45.4",
		"XMP:Subject":              `["holiday","boat"]`,
		"File:FileType":            "NEF",
		"ExifTool:Warning":         "Bad MakerNotes directory",
		"File:ImageDataHash":       "F327C55CB82F5877303559AF3F67DCD6",
		"File:FileName":            "DSC_1234.NEF",
	}
	for name, value := range want {
		if got := raw.tags[name]; got != value {
			t.Errorf("tag %s = %q, want %q", name, got, value)
		}
	}
	if raw.err != nil {
		// A warning is evidence, not a failure: the caller classifies it.
		t.Errorf("a warning became an error: %v", raw.err)
	}

	clip := entries[1]
	if clip.key != "clip.mov" {
		t.Errorf("key = %q, want the ./ normalized away", clip.key)
	}
	if clip.hash != "" {
		t.Errorf("hash = %q, want empty for a format with none", clip.hash)
	}
	if clip.tags["QuickTime:Duration"] != "12" {
		t.Errorf("numeric tag = %q, want 12", clip.tags["QuickTime:Duration"])
	}

	if entries[2].err == nil {
		t.Error("an ExifTool:Error entry did not become a per-file error")
	} else if !strings.Contains(entries[2].err.Error(), "File is empty") {
		t.Errorf("error = %v, want it to quote ExifTool", entries[2].err)
	}
}

func TestParseEntriesEmptyAndUnparsable(t *testing.T) {
	entries, err := parseEntries("   \n")
	if err != nil || entries != nil {
		t.Errorf("parseEntries(blank) = %v, %v; want nil, nil", entries, err)
	}
	if _, err := parseEntries("Error: not JSON at all"); err == nil {
		t.Error("parseEntries accepted output that is not JSON")
	}
}

func TestEntryTakeCopiesForARepeatedPath(t *testing.T) {
	entries, err := parseEntries(cannedAnswer)
	if err != nil {
		t.Fatalf("parseEntries: %v", err)
	}
	first := entries[0].take()
	second := entries[0].take()
	first["EXIF:Make"] = "changed"
	if second["EXIF:Make"] == "changed" {
		t.Error("two results for one path share a tag map")
	}
}

func TestAttributeErrorsToFiles(t *testing.T) {
	notes := []string{
		"Error: File not found - /archive/gone.jpg",
		"Warning: [minor] Skipped bad data - /archive/odd.jpg",
		"    2 image files read",
		"Error: Unknown file type - ./notes.txt",
	}
	said := attribute(notes)
	if got := said["/archive/gone.jpg"]; got != "File not found - /archive/gone.jpg" {
		t.Errorf("attributed %q", got)
	}
	if got := said["notes.txt"]; got == "" {
		t.Error("an error on a ./ path was not attributed")
	}
	if _, ok := said["/archive/odd.jpg"]; ok {
		t.Error("a warning was attributed as a refusal")
	}
}

func TestFenceRoundTrip(t *testing.T) {
	for _, seq := range []int{1, 42, 1000000} {
		got, ok := parseFence(fenceFor(seq))
		if !ok || got != seq {
			t.Errorf("parseFence(%s) = %d, %v", fenceFor(seq), got, ok)
		}
	}
	for _, line := range []string{"", "{ready}", "{readyx}", "ready1", "  {ready1}", `"Tag": "{ready1}"`} {
		if _, ok := parseFence(line); ok {
			t.Errorf("parseFence(%q) matched", line)
		}
	}
}

func TestReadToToleratesBothLineEndings(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		stream := strings.Join([]string{"[{", `  "SourceFile": "a.jpg"`, "}]", "{ready3}", "leftover"}, eol) + eol
		text, err := readTo(bufio.NewReader(strings.NewReader(stream)), "{ready3}")
		if err != nil {
			t.Fatalf("readTo(%q endings): %v", eol, err)
		}
		if strings.Contains(text, "leftover") {
			t.Errorf("readTo read past the sentinel: %q", text)
		}
		if !strings.Contains(text, "SourceFile") {
			t.Errorf("readTo lost the answer: %q", text)
		}
	}
}

func TestReadToReportsAnEarlyExit(t *testing.T) {
	_, err := readTo(bufio.NewReader(strings.NewReader("[{}]\n")), "{ready1}")
	if !errors.Is(err, errExited) {
		t.Errorf("readTo error = %v, want errExited", err)
	}
}

func TestInstallHintNamesAPackageManager(t *testing.T) {
	// The hint is what a first-time user acts on, so it must say
	// something concrete on every platform this builds for.
	if hint := installHint(); hint == "" || !strings.Contains(hint, "exiftool") {
		t.Errorf("installHint() = %q", hint)
	}
}

func TestCheckProbe(t *testing.T) {
	answer := func(hash string) string {
		return fmt.Sprintf(`[{"SourceFile":"probe.png","ExifTool:ExifToolVersion":13.55,"File:ImageDataHash":%q}]`, hash)
	}
	if err := checkProbe("exiftool", answer(probeHash)); err != nil {
		t.Errorf("checkProbe on a correct answer = %v", err)
	}
	err := checkProbe("exiftool", `[{"SourceFile":"probe.png","ExifTool:ExifToolVersion":11.90}]`)
	if err == nil || !strings.Contains(err.Error(), "ImageDataHash") {
		t.Errorf("checkProbe with no hash = %v, want it to name ImageDataHash", err)
	}
	// A different digest means a different identity for every file
	// this build would ever name; it can never pass quietly.
	err = checkProbe("exiftool", answer("0123456789abcdef0123456789abcdef"))
	if err == nil || !strings.Contains(err.Error(), probeHash) {
		t.Errorf("checkProbe with a foreign digest = %v, want it to name the expected one", err)
	}
	if err := checkProbe("exiftool", "[]"); err == nil {
		t.Error("checkProbe accepted an answer with no results")
	}
	if err := checkProbe("exiftool", "not json"); err == nil {
		t.Error("checkProbe accepted an answer that is not JSON")
	}
}

func TestAvailableErrorCarriesTheHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := Available()
	if err == nil {
		t.Fatal("Available() found exiftool on an empty PATH")
	}
	if !strings.Contains(err.Error(), installHint()) {
		t.Errorf("Available() = %v, want it to name the install hint %q", err, installHint())
	}
}

// The tests below drive the real -stay_open protocol against a stand-in
// process, so they run with or without ExifTool installed.

func helperPool(t *testing.T, size int, mode ...string) *Pool {
	t.Helper()
	argv := append([]string{os.Args[0], "-test.run=^TestHelperProcess$", "--"}, mode...)
	pool, err := newPool(argv, size)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func TestReadKeepsOrderAndReportsPerFileTrouble(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	pool := helperPool(t, 2, "echo", log)

	// The repeated path sits in the same chunk as its twin; the
	// batch spans both processes.
	paths := []string{"a.jpg", "missing.jpg", "a.jpg", "b.jpg", "-dash.jpg"}
	got := pool.Read(paths, []string{"DateTimeOriginal"})
	if len(got) != len(paths) {
		t.Fatalf("Read returned %d results for %d paths", len(got), len(paths))
	}
	for i, md := range got {
		if md.Path != paths[i] {
			t.Fatalf("result %d is for %q, want %q", i, md.Path, paths[i])
		}
	}
	if got[1].Err == nil {
		t.Error("a file the process refused came back without an error")
	} else if !strings.Contains(got[1].Err.Error(), "File not found") {
		t.Errorf("error = %v, want the reason the process gave", got[1].Err)
	}
	for _, i := range []int{0, 2, 3, 4} {
		if got[i].Err != nil {
			t.Errorf("result %d (%s): %v", i, got[i].Path, got[i].Err)
		}
		if got[i].ImageDataHash != strings.ToLower(helperHash) {
			t.Errorf("result %d hash = %q, want it lowercased", i, got[i].ImageDataHash)
		}
		if base := filepath.Base(got[i].Path); got[i].Tags["File:FileName"] != base {
			t.Errorf("result %d carries tags for %q", i, got[i].Tags["File:FileName"])
		}
	}
	// A repeated path must not hand the same map out twice.
	got[0].Tags["File:FileName"] = "rewritten"
	if got[2].Tags["File:FileName"] == "rewritten" {
		t.Error("two results share one tag map")
	}
}

func TestReadRefusesANewlinePathWithoutSendingIt(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	pool := helperPool(t, 1, "echo", log)

	smuggled := "a.jpg\n-overwrite_original\n-DateTimeOriginal=2001:01:01 00:00:00\nb.jpg"
	got := pool.Read([]string{"good.jpg", smuggled, "", "also-good.jpg"}, nil)

	if !errors.Is(got[1].Err, ErrNewlineInPath) {
		t.Errorf("result 1 error = %v, want ErrNewlineInPath", got[1].Err)
	}
	if !errors.Is(got[2].Err, ErrEmptyPath) {
		t.Errorf("result 2 error = %v, want ErrEmptyPath", got[2].Err)
	}
	for _, i := range []int{0, 3} {
		if got[i].Err != nil {
			t.Errorf("a refusal spoiled result %d: %v", i, got[i].Err)
		}
	}

	seen, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading the argument log: %v", err)
	}
	for _, fragment := range []string{"-overwrite_original", "-DateTimeOriginal", "b.jpg"} {
		if strings.Contains(string(seen), fragment) {
			t.Fatalf("%q reached the process:\n%s", fragment, seen)
		}
	}
}

func TestReadRefusesAnUnusableTag(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	pool := helperPool(t, 1, "echo", log)

	paths := []string{"a.jpg", "b.jpg"}
	got := pool.Read(paths, []string{"DateTimeOriginal", "Artist=nobody"})
	for i, md := range got {
		if !errors.Is(md.Err, ErrBadTag) {
			t.Errorf("result %d error = %v, want ErrBadTag", i, md.Err)
		}
	}
	if seen, err := os.ReadFile(log); err == nil && len(seen) > 0 {
		t.Fatalf("a read went ahead with an unusable tag:\n%s", seen)
	}
}

func TestReadOnAWedgedProcessGivesUp(t *testing.T) {
	pool := helperPool(t, 1, "wedged")
	pool.base, pool.perFile = 300*time.Millisecond, 0

	done := make(chan []Metadata, 1)
	go func() { done <- pool.Read([]string{"a.jpg", "b.jpg"}, nil) }()
	select {
	case got := <-done:
		for i, md := range got {
			if md.Err == nil {
				t.Errorf("result %d came back without an error", i)
			}
		}
		if len(got) != 2 {
			t.Errorf("Read returned %d results, want 2", len(got))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Read hung on a process that never answered")
	}

	// The process is spent; it must not be handed more work.
	after := pool.Read([]string{"c.jpg"}, nil)
	if after[0].Err == nil {
		t.Error("a read went to a process already known to be dead")
	}
}

func TestCloseIsIdempotentAndStopsReads(t *testing.T) {
	pool := helperPool(t, 2, "echo", filepath.Join(t.TempDir(), "args"))
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	got := pool.Read([]string{"a.jpg"}, nil)
	if !errors.Is(got[0].Err, ErrClosed) {
		t.Errorf("Read after Close = %v, want ErrClosed", got[0].Err)
	}
}

func TestConcurrentReadsShareThePool(t *testing.T) {
	pool := helperPool(t, 3, "echo", filepath.Join(t.TempDir(), "args"))
	paths := make([]string, 60)
	for i := range paths {
		paths[i] = fmt.Sprintf("photo%03d.jpg", i)
	}
	done := make(chan struct{})
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for _, md := range pool.Read(paths, []string{"DateTimeOriginal"}) {
				if md.Err != nil {
					t.Errorf("%s: %v", md.Path, md.Err)
				}
			}
		}()
	}
	for range 4 {
		<-done
	}
}

// helperHash is the value the stand-in process reports, uppercase so
// that the driver's lowercasing is visible.
const helperHash = "AABBCCDDEEFF00112233445566778899"

// TestHelperProcess is not a test: re-executed with a mode argument, it
// stands in for exiftool so the protocol can be driven without it.
func TestHelperProcess(t *testing.T) {
	args := os.Args
	at := slices.Index(args, "--")
	if at < 0 || at+1 >= len(args) {
		t.Skip("not running as a stand-in process")
	}
	switch mode := args[at+1]; mode {
	case "wedged":
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "echo":
		replay(os.Stdin, os.Stdout, os.Stderr, args[at+2])
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", mode)
		os.Exit(2)
	}
	os.Exit(0)
}

// replay speaks the -stay_open protocol: it collects one argument per
// line, and answers each -execute with JSON and the sentinel.
func replay(in io.Reader, out, errOut io.Writer, log string) {
	valued := map[string]bool{"-api": true, "-charset": true, "-echo4": true}
	var files []string
	var fence string
	var pending string

	record, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		os.Exit(2)
	}
	defer func() { _ = record.Close() }()

	lines := bufio.NewScanner(in)
	for lines.Scan() {
		arg := lines.Text()
		_, _ = fmt.Fprintln(record, arg)
		switch {
		case pending == "-echo4":
			fence, pending = arg, ""
		case pending != "":
			pending = ""
		case arg == "-stay_open":
			pending = arg
		case strings.HasPrefix(arg, "-execute"):
			answer(out, errOut, files, fence, strings.TrimPrefix(arg, "-execute"))
			files, fence = nil, ""
		case valued[arg]:
			pending = arg
		case strings.HasPrefix(arg, "-"):
		default:
			files = append(files, arg)
		}
	}
}

func answer(out, errOut io.Writer, files []string, fence, seq string) {
	docs := make([]map[string]any, 0, len(files))
	for _, file := range files {
		if filepath.Base(file) == "missing.jpg" {
			_, _ = fmt.Fprintf(errOut, "Error: File not found - %s\n", file)
			continue
		}
		docs = append(docs, map[string]any{
			"SourceFile":            file,
			"File:FileName":         filepath.Base(file),
			"File:ImageDataHash":    helperHash,
			"EXIF:DateTimeOriginal": "2026:07:03 15:07:27",
		})
	}
	encoded, err := json.Marshal(docs)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return
	}
	_, _ = fmt.Fprintf(errOut, "    %d image files read\n%s\n", len(docs), fence)
	_, _ = fmt.Fprintf(out, "%s\n{ready%s}\n", encoded, seq)
}
