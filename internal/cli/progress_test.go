package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stampla/stampla/internal/engine"
)

// TestProgressOnlyOnATerminal is the whole rule: a redirected stderr is
// a log file, and a log file full of carriage returns is not a log.
func TestProgressOnlyOnATerminal(t *testing.T) {
	var stderr strings.Builder
	p := &progressLine{out: &out{w: &stderr}}
	p.emit(engine.PhaseRead, 1, 2, filepath.Join(testCard, "DSC_1234.NEF"))
	p.clear()
	if stderr.Len() != 0 {
		t.Errorf("progress was drawn on a stream that is not a terminal: %q", stderr.String())
	}
}

func TestProgressRewritesOneLine(t *testing.T) {
	var stderr strings.Builder
	p := &progressLine{out: &out{w: &stderr}, on: true}

	p.emit(engine.PhaseRead, 1, 10, filepath.Join(testCard, "DSC_1234.NEF"))
	first := stderr.String()
	if !strings.HasPrefix(first, "\rread 1/10 DSC_1234.NEF") {
		t.Errorf("progress = %q, want the phase, the count and the file", first)
	}
	if strings.Contains(first, "\n") {
		t.Errorf("progress ended its line: %q", first)
	}

	// The throttle: an engine reporting per group reports thousands of
	// times a second, and none of those are readable.
	stderr.Reset()
	p.emit(engine.PhaseRead, 2, 10, filepath.Join(testCard, "DSC_1235.NEF"))
	if stderr.Len() != 0 {
		t.Errorf("progress redrew inside the interval: %q", stderr.String())
	}

	// Except the last event of a phase, which is the one that says the
	// phase finished.
	p.emit(engine.PhaseRead, 10, 10, "")
	if !strings.Contains(stderr.String(), "read 10/10") {
		t.Errorf("the end of a phase was swallowed by the throttle: %q", stderr.String())
	}

	// And clearing covers whatever was there, so a report never lands on
	// top of a half-erased count.
	stderr.Reset()
	p.clear()
	cleared := stderr.String()
	if !strings.HasPrefix(cleared, "\r") || !strings.HasSuffix(cleared, "\r") ||
		strings.TrimSpace(cleared) != "" {
		t.Errorf("clear() = %q, want the line covered and the cursor returned", cleared)
	}
	stderr.Reset()
	p.clear()
	if stderr.Len() != 0 {
		t.Errorf("clear() drew over a line that was not there: %q", stderr.String())
	}
}

func TestProgressFitsTheLine(t *testing.T) {
	var stderr strings.Builder
	p := &progressLine{out: &out{w: &stderr}, on: true}
	p.emit(engine.PhaseApply, 1, 2, filepath.Join(testCard, strings.Repeat("é", 200)+".nef"))
	line := strings.TrimPrefix(stderr.String(), "\r")
	// Cut by runes: a filename cut mid-character leaves a broken
	// sequence no later write repairs.
	if runes := []rune(line); len(runes) != progressWidth {
		t.Errorf("progress is %d runes wide, want %d: %q", len(runes), progressWidth, line)
	}
}

func TestProgressText(t *testing.T) {
	tests := []struct {
		phase engine.Phase
		done  int
		total int
		path  string
		want  string
	}{
		{phase: engine.PhaseRead, done: 3, total: 10, path: filepath.Join(testCard, "a.nef"), want: "read 3/10 a.nef"},
		{phase: engine.PhaseHash, done: 0, total: 0, want: "hash"},
		{phase: engine.PhaseVerify, done: 4, total: 0, want: "verify 4"},
		{phase: engine.PhaseApply, done: 2, total: 2, want: "apply 2/2"},
	}
	for _, tc := range tests {
		if got := progressText(tc.phase, tc.done, tc.total, tc.path); got != tc.want {
			t.Errorf("progressText(%s, %d, %d, %q) = %q, want %q",
				tc.phase, tc.done, tc.total, tc.path, got, tc.want)
		}
	}
}

func TestProgressIntervalIsShortEnoughToRead(t *testing.T) {
	if progressInterval < 10*time.Millisecond || progressInterval > time.Second {
		t.Errorf("progressInterval = %v, which is not a rate a person reads at", progressInterval)
	}
}
