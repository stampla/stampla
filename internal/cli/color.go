package cli

// The escapes, written out rather than pulled in: three sequences are
// not a dependency, and a report that colors damage red is the whole
// requirement.
const (
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
	ansiBold  = "\x1b[1m"
)

// palette turns a piece of a report into color, or leaves it exactly as
// it was.
type palette struct{ on bool }

func (p palette) red(text string) string  { return p.wrap(ansiRed, text) }
func (p palette) bold(text string) string { return p.wrap(ansiBold, text) }

func (p palette) wrap(escape, text string) string {
	if !p.on || text == "" {
		return text
	}
	return escape + text + ansiReset
}

// colorOn resolves --color against the terminal and the environment.
//
// always and never are answers, not preferences: a user who asked for
// color in a pipe is piping into something that renders it, and NO_COLOR
// speaks for the environment rather than for the command line. auto is
// the only mode that asks anything, and what it asks is whether stdout —
// where the report goes — is a terminal.
func colorOn(mode string, stdoutTTY bool, noColor string) bool {
	switch mode {
	case colorAlways:
		return true
	case colorNever:
		return false
	default:
		return stdoutTTY && noColor == ""
	}
}
