package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/stampla/stampla/internal/engine"
)

// progressWidth caps the line so that it fits a narrow terminal without
// wrapping. A wrapped progress line is a line \r cannot take back, and
// what is left on the screen is a trail of half-sentences.
const progressWidth = 72

// progressInterval is how often the line is rewritten. The engine
// reports per group, which on a fast archive is thousands of times a
// second, and none of those are readable.
const progressInterval = 80 * time.Millisecond

// progressLine is a single line on stderr, rewritten in place.
//
// It exists only when stderr is a terminal: a redirected stderr is a log
// file, and a log file full of carriage returns is not a log. Under
// --porcelain there is no progressLine at all — the events go into the
// stream instead.
type progressLine struct {
	out   *out
	on    bool
	last  time.Time
	width int
}

func (p *progressLine) emit(phase engine.Phase, done, total int, path string) {
	if p == nil || !p.on {
		return
	}
	now := time.Now()
	// The last event of a phase is always shown: it is the one that says
	// the phase finished, and a throttle that swallowed it would leave
	// the line claiming work that is done is still running.
	if total > 0 && done < total && now.Sub(p.last) < progressInterval {
		return
	}
	p.last = now
	p.write(progressText(phase, done, total, path))
}

// clear takes the line down before anything else is printed, so a report
// never lands on top of a half-erased count.
func (p *progressLine) clear() {
	if p == nil || !p.on || p.width == 0 {
		return
	}
	p.out.text("\r" + strings.Repeat(" ", p.width) + "\r")
	p.width = 0
}

// write rewrites the line, padded to cover whatever was there before.
// Padding rather than an erase escape: this has to work on a console
// that renders no escapes at all.
func (p *progressLine) write(text string) {
	// Cut by runes, not by bytes: a filename cut mid-character leaves a
	// broken sequence on the terminal that no later write repairs.
	runes := []rune(text)
	if len(runes) > progressWidth {
		runes = runes[:progressWidth]
		text = string(runes)
	}
	pad := ""
	if p.width > len(runes) {
		pad = strings.Repeat(" ", p.width-len(runes))
	}
	p.out.text("\r" + text + pad)
	p.width = len(runes)
}

// progressText is one phase's line: what is happening, how far along it
// is, and which file it is on.
func progressText(phase engine.Phase, done, total int, path string) string {
	head := string(phase)
	if total > 0 {
		head = fmt.Sprintf("%s %d/%d", phase, done, total)
	} else if done > 0 {
		head = fmt.Sprintf("%s %d", phase, done)
	}
	if path == "" {
		return head
	}
	return head + " " + filepath.Base(path)
}
