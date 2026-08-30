package layout

import (
	"strings"
	"testing"
	"time"
)

// capture is the reference capture time for rendering tests: a
// single-digit month and day, so zero padding is exercised.
var capture = time.Date(2026, time.July, 3, 15, 7, 27, 0, time.UTC)

func TestParsePatternRenders(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
		wantStr string
	}{
		{"flat", "", "", ""},
		{"default", "{yyyy}/{yyyy}-{mm}", "2026/2026-07", "{yyyy}/{yyyy}-{mm}"},
		{"year", "{yyyy}", "2026", "{yyyy}"},
		{"month", "{mm}", "07", "{mm}"},
		{"day", "{dd}", "03", "{dd}"},
		{"year month token", "{yyyy-mm}", "2026-07", "{yyyy-mm}"},
		{"full date token", "{yyyy-mm-dd}", "2026-07-03", "{yyyy-mm-dd}"},
		{"literal only", "Capture", "Capture", "Capture"},
		{"literal segments", "Photos/Raw", "Photos/Raw", "Photos/Raw"},
		{"literal around token", "Y{yyyy}x", "Y2026x", "Y{yyyy}x"},
		{"tokens adjacent", "{yyyy}{mm}{dd}", "20260703", "{yyyy}{mm}{dd}"},
		{"deep", "{yyyy}/{yyyy-mm}/{yyyy-mm-dd}", "2026/2026-07/2026-07-03", "{yyyy}/{yyyy-mm}/{yyyy-mm-dd}"},
		{"literal and tokens mixed", "Archive/{yyyy}/shoot-{mm}-{dd}", "Archive/2026/shoot-07-03", "Archive/{yyyy}/shoot-{mm}-{dd}"},
		{"spaces in literal", "My Photos/{yyyy}", "My Photos/2026", "My Photos/{yyyy}"},
		{"unicode literal", "Zdjęcia/{yyyy}", "Zdjęcia/2026", "Zdjęcia/{yyyy}"},
		{"hidden segment", ".raw/{yyyy}", ".raw/2026", ".raw/{yyyy}"},
		{"backslash separator normalized", `{yyyy}\{mm}`, "2026/07", "{yyyy}/{mm}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePattern(tt.pattern)
			if err != nil {
				t.Fatalf("ParsePattern(%q) = %v, want no error", tt.pattern, err)
			}
			if got := p.Dir(capture); got != tt.want {
				t.Errorf("Dir() = %q, want %q", got, tt.want)
			}
			if got := p.String(); got != tt.wantStr {
				t.Errorf("String() = %q, want %q", got, tt.wantStr)
			}
			if got := p.Dir(capture); strings.Contains(got, `\`) {
				t.Errorf("Dir() = %q, must never contain a backslash", got)
			}
			if want := tt.want == ""; p.IsFlat() != want {
				t.Errorf("IsFlat() = %v, want %v", p.IsFlat(), want)
			}
		})
	}
}

func TestParsePatternRejects(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr string
	}{
		{"unknown token", "{shoot}/{yyyy}", "unknown token"},
		{"unknown date token", "{YYYY}", "unknown token"},
		{"empty token", "{}", "unknown token"},
		{"unterminated token", "{yyyy", "unterminated token"},
		{"unmatched close", "yyyy}", "unmatched"},
		{"absolute unix", "/{yyyy}", "relative"},
		{"absolute backslash", `\{yyyy}`, "relative"},
		{"absolute drive", `C:\{yyyy}`, "relative"},
		{"absolute drive slash", "C:/photos", "relative"},
		{"parent segment", "{yyyy}/..", "leave the archive"},
		{"parent segment first", "../{yyyy}", "leave the archive"},
		{"dot segment", "./{yyyy}", "not a directory name"},
		{"empty segment", "{yyyy}//{mm}", "empty path segment"},
		{"trailing separator", "{yyyy}/", "empty path segment"},
		{"only separator", "/", "relative"},
		{"colon", "{yyyy}:{mm}", "invalid character"},
		{"question mark", "{yyyy}?", "invalid character"},
		{"asterisk", "*", "invalid character"},
		{"pipe", "a|b", "invalid character"},
		{"quote", `say"what`, "invalid character"},
		{"angle brackets", "<{yyyy}>", "invalid character"},
		{"newline", "{yyyy}\n{mm}", "control character"},
		{"tab", "{yyyy}\t", "control character"},
		{"trailing dot", "{yyyy}.", "ends in"},
		{"trailing space", "{yyyy} /{mm}", "ends in"},
		{"reserved device", "{yyyy}/NUL", "reserved device name"},
		{"reserved device lowercase", "con/{yyyy}", "reserved device name"},
		{"reserved device with extension", "COM1.d", "reserved device name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePattern(tt.pattern)
			if err == nil {
				t.Fatalf("ParsePattern(%q) = %q, want an error", tt.pattern, p.Dir(capture))
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "invalid layout") {
				t.Errorf("error = %q, want it to name the offending layout", err)
			}
		})
	}
}

func TestPatternZeroValueIsFlat(t *testing.T) {
	var p Pattern
	if got := p.Dir(capture); got != "" {
		t.Errorf("zero Pattern Dir() = %q, want %q", got, "")
	}
	if got := p.String(); got != "" {
		t.Errorf("zero Pattern String() = %q, want %q", got, "")
	}
	if !p.IsFlat() {
		t.Error("zero Pattern IsFlat() = false, want true")
	}
}

func TestPatternDirIsDeterministic(t *testing.T) {
	p, err := ParsePattern("{yyyy}/{yyyy-mm-dd}")
	if err != nil {
		t.Fatal(err)
	}
	// Same instant in a different zone is a different wall clock, and
	// the wall clock is what names the file.
	utc := time.Date(2026, time.January, 1, 0, 30, 0, 0, time.UTC)
	east := utc.In(time.FixedZone("plus2", 2*60*60))
	if got, want := p.Dir(utc), "2026/2026-01-01"; got != want {
		t.Errorf("Dir(utc) = %q, want %q", got, want)
	}
	if got, want := p.Dir(east), "2026/2026-01-01"; got != want {
		t.Errorf("Dir(east) = %q, want %q", got, want)
	}
	if got, want := p.Dir(utc.Add(-time.Hour)), "2025/2025-12-31"; got != want {
		t.Errorf("Dir(previous year) = %q, want %q", got, want)
	}
}

func TestDefaultPatternParses(t *testing.T) {
	p, err := ParsePattern(DefaultPattern)
	if err != nil {
		t.Fatalf("ParsePattern(DefaultPattern) = %v", err)
	}
	if got, want := p.Dir(capture), "2026/2026-07"; got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestMustParsePatternPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParsePattern did not panic on an invalid pattern")
		}
	}()
	MustParsePattern("{shoot}")
}
