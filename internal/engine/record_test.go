package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/testutil"
)

// A source is never removed on the strength of a record that was not
// written. The receipt is the only surviving account of what a file used
// to be called, so deleting an original whose old name went unrecorded
// destroys information no re-run can recover.
func TestSourcesSurviveAReceiptFailure(t *testing.T) {
	pool := newPool(t)
	source, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, source, map[string]string{"DSC_1234.jpg": "@dated.jpg"})
	testutil.WriteSidecar(t, filepath.Join(source, "DSC_1234.xmp"), testutil.JPEGDate)

	// The receipt cannot be written: something is already in its place
	// that is not a file.
	if err := os.Mkdir(filepath.Join(dest, ReceiptName), 0o755); err != nil {
		t.Fatalf("blocking the receipt: %v", err)
	}

	plan := mustPlan(t, Options{
		Mode: Move, Scan: scanOf(t, source), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	result, err := Apply(plan, ApplyOptions{ForceCrossVolume: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Failed) != 1 {
		t.Fatalf("failures %v, want exactly one", result.Failed)
	}
	for _, want := range []string{"no source was removed", "already present"} {
		if !strings.Contains(result.Failed[0].Err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, result.Failed[0].Err)
		}
	}
	// A caller maps a non-empty Failed to the alarm exit code; the plan's
	// own code cannot see operational trouble.
	if len(result.Applied) != 0 || len(result.Landed) != 0 || result.Members != 0 {
		t.Errorf("a group whose record failed was reported as applied: %v / %v",
			result.Applied, result.Landed)
	}

	// The sources are exactly where they were, both of them.
	wantTree(t, source, "DSC_1234.jpg", "DSC_1234.xmp")
	// The copies are in the archive; nothing was undone that was good.
	wantTree(t, dest,
		dateDir+"/20260703_150727_0a8c8109.jpg",
		dateDir+"/20260703_150727_0a8c8109.xmp")
}

// A filename may hold a tab or a newline. Written raw, the first makes a
// five-field line and the second shatters one record into unparsable
// pieces — and the run reports success, having just destroyed the record
// of what that file was called.
func TestReceiptSurvivesAwkwardNames(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"a tab", "/card/DSC\t1234.jpg"},
		{"a newline", "/card/DSC\n1234.jpg"},
		{"a carriage return", "/card/DSC\r1234.jpg"},
		{"a quote", `/card/"quoted".jpg`},
		{"a backslash", `C:\card\DSC_1234.jpg`},
		{"nothing awkward", "/card/DSC_1234.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ReceiptName)
			target := "/archive/" + jpegName
			if err := appendReceipt(path, []receiptLine{
				{verb: VerbMove, old: tc.path, new: target},
			}); err != nil {
				t.Fatalf("appendReceipt: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.TrimSuffix(string(data), "\n")
			if strings.ContainsAny(text, "\n\r") {
				t.Fatalf("one mutation wrote more than one line: %q", text)
			}
			fields := strings.Split(text, "\t")
			if len(fields) != 4 {
				t.Fatalf("%d fields, want 4: %q", len(fields), fields)
			}
			if got := unquoteField(t, fields[2]); got != tc.path {
				t.Errorf("old path read back as %q, want %q", got, tc.path)
			}
			if got := unquoteField(t, fields[3]); got != target {
				t.Errorf("new path read back as %q, want %q", got, target)
			}
		})
	}
}

// An ordinary path stays plain: a receipt is meant to be read by people
// first, and quoting everything would be a format nobody skims. Only the
// three characters the format could not survive, and a leading quote,
// ask for the quoted form.
func TestReceiptQuotesOnlyWhenItMust(t *testing.T) {
	plain := []string{
		"/card/DSC_1234.jpg",
		"/archive/2026/2026-07/" + jpegName,
		"with spaces.jpg",
		// A backslash is not a trigger, so a Windows path is written
		// exactly as Windows spells it.
		`C:\card\DCIM\DSC_1234.jpg`,
		`\\server\share\DSC_1234.jpg`,
		`a "quoted" middle.jpg`,
	}
	for _, path := range plain {
		if got := receiptField(path); got != path {
			t.Errorf("receiptField(%q) = %q, want it written verbatim", path, got)
		}
	}

	quoted := []string{"a\tb.jpg", "a\nb.jpg", "a\rb.jpg", `"a.jpg`}
	for _, path := range quoted {
		got := receiptField(path)
		if !strings.HasPrefix(got, `"`) {
			t.Errorf("receiptField(%q) = %q, want a quoted field", path, got)
		}
		if back, err := strconv.Unquote(got); err != nil || back != path {
			t.Errorf("receiptField(%q) = %q does not unquote back (%q, %v)", path, got, back, err)
		}
	}

	// A backslash inside a field quoted for another reason is escaped by
	// the quoted form itself, so the two forms still cannot be read as
	// each other.
	windowsWithTab := "C:\\card\\DSC\t1234.jpg"
	got := receiptField(windowsWithTab)
	if !strings.Contains(got, `\\`) {
		t.Errorf("receiptField(%q) = %q, want its backslashes escaped", windowsWithTab, got)
	}
	if strings.ContainsAny(got, "\t\n\r") {
		t.Errorf("receiptField(%q) = %q still holds a raw separator", windowsWithTab, got)
	}
	if back, err := strconv.Unquote(got); err != nil || back != windowsWithTab {
		t.Errorf("receiptField(%q) = %q does not unquote back (%q, %v)",
			windowsWithTab, got, back, err)
	}
}

// unquoteField reads one receipt path field the way a reader would: a
// leading quote means the quoted form, anything else is the path itself.
func unquoteField(t *testing.T, field string) string {
	t.Helper()
	if !strings.HasPrefix(field, `"`) {
		return field
	}
	value, err := strconv.Unquote(field)
	if err != nil {
		t.Fatalf("unquoting %q: %v", field, err)
	}
	return value
}

// And the same, end to end: a member whose name holds a newline reaches
// the receipt through a real run. ExifTool refuses such a name, which
// covers the files it reads — every group master — but a derivative is
// never read, so this is the path that gets there.
func TestReceiptRecordsANewlineInAMemberName(t *testing.T) {
	pool := newPool(t)
	card, dest := t.TempDir(), t.TempDir()
	testutil.Tree(t, card, map[string]string{"VID_0001.mp4": "@date.mp4"})

	awkward := filepath.Join(card, "VID_0001.mp4\nfinal.xmp")
	if !canCreate(t, awkward) {
		t.Skip("this filesystem does not accept a newline in a filename")
	}
	testutil.WriteSidecar(t, awkward, testutil.VideoDate)

	plan := mustPlan(t, Options{
		Mode: Copy, Scan: scanOf(t, card), Dest: dest,
		Resolution: fallbackLayout(t), Pool: pool,
	})
	result := mustApply(t, plan, ApplyOptions{})
	if len(result.Landed) != 2 {
		t.Fatalf("landed %v, want the clip and its sidecar\n%s",
			result.Landed, dumpPlan(plan))
	}

	data, err := os.ReadFile(filepath.Join(dest, ReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != len(result.Landed) {
		t.Fatalf("%d receipt lines for %d landed members: %q",
			len(lines), len(result.Landed), lines)
	}
	found := false
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("%d fields in %q, want 4", len(fields), line)
		}
		if unquoteField(t, fields[2]) == awkward {
			found = true
		}
	}
	if !found {
		t.Errorf("no line records %q: %q", awkward, lines)
	}
}

// canCreate reports whether this filesystem accepts a name at all.
func canCreate(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the probe file: %v", err)
	}
	return true
}
