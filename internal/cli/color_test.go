package cli

import (
	"strings"
	"testing"
)

func TestColorOn(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		stdoutTTY bool
		noColor   string
		want      bool
	}{
		{name: "auto on a terminal", mode: colorAuto, stdoutTTY: true, want: true},
		{name: "auto in a pipe", mode: colorAuto},
		{name: "auto with NO_COLOR", mode: colorAuto, stdoutTTY: true, noColor: "1"},
		{name: "never on a terminal", mode: colorNever, stdoutTTY: true},
		{name: "always in a pipe", mode: colorAlways, want: true},
		// An explicit --color=always is an answer, not a preference:
		// NO_COLOR speaks for the environment, and the command line
		// speaks after it.
		{name: "always with NO_COLOR", mode: colorAlways, noColor: "1", want: true},
		{name: "never with NO_COLOR", mode: colorNever, noColor: "1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := colorOn(tc.mode, tc.stdoutTTY, tc.noColor); got != tc.want {
				t.Errorf("colorOn(%q, %v, %q) = %v, want %v",
					tc.mode, tc.stdoutTTY, tc.noColor, got, tc.want)
			}
		})
	}
}

func TestPalette(t *testing.T) {
	off := palette{}
	if got := off.red("corrupt"); got != "corrupt" {
		t.Errorf("a palette that is off wrapped %q", got)
	}
	on := palette{on: true}
	got := on.red("corrupt")
	if !strings.HasPrefix(got, ansiRed) || !strings.HasSuffix(got, ansiReset) {
		t.Errorf("red(%q) = %q, want it wrapped and reset", "corrupt", got)
	}
	// An empty string is nothing to color, and wrapping it would leave a
	// reset escape sitting in the report on its own.
	if got := on.bold(""); got != "" {
		t.Errorf("bold(\"\") = %q, want nothing", got)
	}
}
