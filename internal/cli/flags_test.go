package cli

import (
	"errors"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name string
		verb string
		args []string
		want options
	}{
		{
			name: "short dry run",
			verb: verbCopy,
			args: []string{"-n", "card", "photos"},
			want: options{dryRun: true, color: colorAuto, args: []string{"card", "photos"}},
		},
		{
			name: "long dry run and yes",
			verb: verbMove,
			args: []string{"--dry-run", "--yes", "card", "photos"},
			want: options{dryRun: true, yes: true, color: colorAuto, args: []string{"card", "photos"}},
		},
		{
			// The distinction the layout chain rests on: no flag asks the
			// destination what it declares, and --layout "" declares flat.
			name: "layout absent",
			verb: verbCopy,
			args: []string{"card", "photos"},
			want: options{color: colorAuto, args: []string{"card", "photos"}},
		},
		{
			name: "layout empty is the flat layout",
			verb: verbCopy,
			args: []string{"--layout", "", "card", "photos"},
			want: options{layoutSet: true, color: colorAuto, args: []string{"card", "photos"}},
		},
		{
			name: "layout given",
			verb: verbCopy,
			args: []string{"--layout={yyyy}", "card", "photos"},
			want: options{layout: "{yyyy}", layoutSet: true, color: colorAuto, args: []string{"card", "photos"}},
		},
		{
			name: "stdin nul separated",
			verb: verbCopy,
			args: []string{"--stdin", "-z", "photos"},
			want: options{stdin: true, nulSep: true, color: colorAuto, args: []string{"photos"}},
		},
		{
			name: "porcelain and workers",
			verb: verbVerify,
			args: []string{"--porcelain", "--workers", "3", "photos"},
			want: options{porcelain: true, workers: 3, color: colorAuto, args: []string{"photos"}},
		},
		{
			name: "color mode",
			verb: verbVerify,
			args: []string{"--color=never", "photos"},
			want: options{color: colorNever, args: []string{"photos"}},
		},
		{
			// "--" is what makes a file named like an option nameable.
			name: "double dash ends the options",
			verb: verbCopy,
			args: []string{"--", "-weird.jpg", "photos"},
			want: options{color: colorAuto, args: []string{"-weird.jpg", "photos"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFlags(tc.verb, tc.args)
			if err != nil {
				t.Fatalf("parseFlags(%q, %q): %v", tc.verb, tc.args, err)
			}
			if got.dryRun != tc.want.dryRun || got.yes != tc.want.yes ||
				got.layout != tc.want.layout || got.layoutSet != tc.want.layoutSet ||
				got.stdin != tc.want.stdin || got.nulSep != tc.want.nulSep ||
				got.porcelain != tc.want.porcelain || got.color != tc.want.color ||
				got.workers != tc.want.workers || !slices.Equal(got.args, tc.want.args) {
				t.Errorf("parseFlags(%q, %q) =\n %+v\nwant\n %+v", tc.verb, tc.args, *got, tc.want)
			}
		})
	}
}

func TestParseFlagsRefusals(t *testing.T) {
	tests := []struct {
		name string
		verb string
		args []string
		want string
	}{
		{"-z without --stdin", verbCopy, []string{"-z", "card", "photos"}, "-z describes"},
		{"unknown color", verbCopy, []string{"--color", "mauve", "card", "photos"}, "--color takes"},
		{"negative workers", verbCopy, []string{"--workers", "-2", "card", "photos"}, "cannot be negative"},
		{"unknown flag", verbCopy, []string{"--recursive", "card", "photos"}, "flag provided but not defined"},
		{"verify takes no layout", verbVerify, []string{"--layout", "{yyyy}", "photos"}, "flag provided but not defined"},
		{"verify takes no dry run", verbVerify, []string{"-n", "photos"}, "flag provided but not defined"},
		{"verify takes no stdin", verbVerify, []string{"--stdin", "photos"}, "flag provided but not defined"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFlags(tc.verb, tc.args)
			if err == nil {
				t.Fatalf("parseFlags(%q, %q) was accepted", tc.verb, tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseFlags(%q, %q) = %v, want it to mention %q", tc.verb, tc.args, err, tc.want)
			}
		})
	}
}

func TestParseFlagsHelp(t *testing.T) {
	for _, verb := range []string{verbCopy, verbMove, verbVerify} {
		for _, arg := range []string{"-h", "--help"} {
			if _, err := parseFlags(verb, []string{arg}); !errors.Is(err, flag.ErrHelp) {
				t.Errorf("parseFlags(%q, [%q]) = %v, want flag.ErrHelp", verb, arg, err)
			}
		}
	}
}

func TestInputs(t *testing.T) {
	tests := []struct {
		name        string
		opts        options
		wantSources []string
		wantDest    string
		wantErr     string
	}{
		{
			name:        "inputs then destination",
			opts:        options{args: []string{"card", "inbox", "photos"}},
			wantSources: []string{"card", "inbox"},
			wantDest:    "photos",
		},
		{
			name:     "stdin takes only a destination",
			opts:     options{stdin: true, args: []string{"photos"}},
			wantDest: "photos",
		},
		{
			name:    "stdin with an input as well",
			opts:    options{stdin: true, args: []string{"card", "photos"}},
			wantErr: "--stdin takes the destination and nothing else",
		},
		{
			name:    "no destination",
			opts:    options{args: []string{"card"}},
			wantErr: "no destination",
		},
		{
			name:    "nothing at all",
			opts:    options{args: nil},
			wantErr: "no inputs and no destination",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sources, dest, err := tc.opts.inputs(verbCopy, nil)
			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("inputs() = %v, want it to mention %q", err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("inputs(): %v", err)
			case !slices.Equal(sources, tc.wantSources) || dest != tc.wantDest:
				t.Errorf("inputs() = %q, %q, want %q, %q", sources, dest, tc.wantSources, tc.wantDest)
			}
		})
	}
}

// TestInputsStdinNeedsAPipe covers the one case that needs a real
// character device: --stdin with a terminal behind it is a person who
// meant something else, and the run says so instead of waiting.
func TestInputsStdinNeedsAPipe(t *testing.T) {
	device, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no null device to stand in for a terminal: %v", err)
	}
	defer func() { _ = device.Close() }()
	if !isTerminal(device) {
		t.Skip("the null device is not a character device here")
	}

	opts := options{stdin: true, args: []string{"photos"}}
	_, _, err = opts.inputs(verbCopy, device)
	if err == nil || !strings.Contains(err.Error(), "standard input is a terminal") {
		t.Fatalf("inputs() = %v, want the terminal refusal", err)
	}
	if !strings.Contains(err.Error(), "-print0") {
		t.Errorf("inputs() = %v, want it to show how to pipe a list", err)
	}
}

func TestDestArgs(t *testing.T) {
	tests := []struct {
		args     []string
		wantSrc  string
		wantDest string
		wantErr  bool
	}{
		{args: []string{"photos"}, wantDest: "photos"},
		{args: []string{"card", "photos"}, wantSrc: "card", wantDest: "photos"},
		{args: nil, wantErr: true},
		{args: []string{"a", "b", "c"}, wantErr: true},
	}

	for _, tc := range tests {
		opts := options{args: tc.args}
		src, dest, err := opts.destArgs()
		switch {
		case tc.wantErr:
			if err == nil {
				t.Errorf("destArgs(%q) was accepted", tc.args)
			}
		case err != nil:
			t.Errorf("destArgs(%q): %v", tc.args, err)
		case src != tc.wantSrc || dest != tc.wantDest:
			t.Errorf("destArgs(%q) = %q, %q, want %q, %q", tc.args, src, dest, tc.wantSrc, tc.wantDest)
		}
	}
}

func TestDestDir(t *testing.T) {
	dir := t.TempDir()
	if err := destDir(verbCopy, dir); err != nil {
		t.Errorf("destDir(%s): %v", dir, err)
	}

	file := dir + string(os.PathSeparator) + "DSC_0009.jpg"
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", file, err)
	}
	err := destDir(verbCopy, file)
	if err == nil {
		t.Fatalf("destDir(%s) accepted a file as a destination", file)
	}
	// The forgotten destination: a glob ate it, and the last argument is
	// a photograph. The message has to name it.
	if !strings.Contains(err.Error(), "DSC_0009.jpg") || !strings.Contains(err.Error(), "glob") {
		t.Errorf("destDir(file) = %v, want it to name the argument and the glob", err)
	}

	if err := destDir(verbCopy, dir+string(os.PathSeparator)+"nowhere"); err == nil {
		t.Error("destDir accepted a destination that does not exist")
	}
}

func TestDispatchUsage(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "no arguments", args: nil, wantCode: 64, wantErr: "usage:"},
		{name: "unknown verb", args: []string{"import"}, wantCode: 64, wantErr: `unknown verb "import"`},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "usage:"},
		{name: "help for a verb", args: []string{"help", "cp"}, wantCode: 0, wantOut: "stampla cp /Volumes/NIKON/DCIM /photos"},
		{name: "help for a non-verb", args: []string{"help", "import"}, wantCode: 64, wantErr: "no such verb"},
		{name: "verb help flag", args: []string{"verify", "--help"}, wantCode: 0, wantOut: "usage: stampla verify"},
		{name: "unknown flag", args: []string{"cp", "--recursive", "a", "b"}, wantCode: 64, wantErr: "not defined"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, nil, tc.args...)
			if got.code != tc.wantCode {
				t.Errorf("dispatch(%q) = %d, want %d\n%s%s", tc.args, got.code, tc.wantCode, got.stdout, got.stderr)
			}
			if tc.wantOut != "" && !strings.Contains(got.stdout, tc.wantOut) {
				t.Errorf("dispatch(%q) stdout = %q, want it to contain %q", tc.args, got.stdout, tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(got.stderr, tc.wantErr) {
				t.Errorf("dispatch(%q) stderr = %q, want it to contain %q", tc.args, got.stderr, tc.wantErr)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	got := runCLI(t, nil, "version")
	if got.code != 0 {
		t.Fatalf("version = %d, want 0", got.code)
	}
	if !strings.HasPrefix(got.stdout, "stampla test\n") {
		t.Errorf("version stdout = %q, want it to lead with the version", got.stdout)
	}
	// Either the version or the reason there is none, but never silence:
	// which ExifTool named these files is evidence about the files.
	if !strings.Contains(got.stdout, "exiftool") {
		t.Errorf("version stdout = %q, want it to say something about exiftool", got.stdout)
	}
}

func TestQuotePattern(t *testing.T) {
	if got := quotePattern(""); got != `"" (flat)` {
		t.Errorf("quotePattern(\"\") = %q, want the flat layout named", got)
	}
	if got := quotePattern("{yyyy}"); got != "{yyyy}" {
		t.Errorf("quotePattern({yyyy}) = %q", got)
	}
}
