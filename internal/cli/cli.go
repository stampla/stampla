package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/exif"
	"github.com/stampla/stampla/internal/finding"
)

// The verbs, spelled once.
const (
	verbCopy   = "cp"
	verbMove   = "mv"
	verbVerify = "verify"
)

// Run executes one command line and returns the process exit code.
//
// Every stream the interface uses is a parameter: the report goes to
// stdout, usage and progress and prompts to stderr, and a --stdin file
// list is read from stdin. Nothing here reaches for os.Stdout, so the
// whole interface — including the exit code, which is returned rather
// than taken — runs inside a test with no subprocess.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{
		version:   version,
		stdin:     stdin,
		out:       &out{w: stdout},
		errOut:    &out{w: stderr},
		stdoutTTY: isTerminal(stdout),
		stderrTTY: isTerminal(stderr),
		terminal:  controllingTerminal,
	}
	defer e.closeTerminal()
	return e.dispatch(args)
}

// env is one run's world: its streams, what it knows about them, and
// the terminal a confirmation is asked on.
type env struct {
	version string
	stdin   io.Reader
	out     *out
	errOut  *out

	stdoutTTY bool
	stderrTTY bool

	// terminal opens the controlling terminal. It is a field so a test
	// can state that there is none without depending on whether the test
	// binary happens to have been started from one.
	terminal func() (io.ReadCloser, error)
	tty      io.ReadCloser
	answers  *bufio.Reader
}

// dispatch routes one command line to its verb.
func (e *env) dispatch(args []string) int {
	if len(args) == 0 {
		e.errOut.text(rootUsage)
		return finding.ExitUsage
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case verbCopy:
		return e.mutate(engine.Copy, rest)
	case verbMove:
		return e.mutate(engine.Move, rest)
	case verbVerify:
		return e.verify(rest)
	case "version", "--version":
		return e.showVersion()
	case "help", "--help", "-h":
		return e.help(rest)
	default:
		e.errOut.printf("stampla: unknown verb %q\n\n", verb)
		e.errOut.text(rootUsage)
		return finding.ExitUsage
	}
}

// help prints one verb's usage, or the whole surface.
func (e *env) help(args []string) int {
	if len(args) == 0 {
		e.out.text(rootUsage)
		return finding.ExitConverged
	}
	text, ok := usageFor(args[0])
	if !ok {
		e.errOut.printf("stampla help: no such verb %q\n\n", args[0])
		e.errOut.text(rootUsage)
		return finding.ExitUsage
	}
	e.out.text(text)
	return finding.ExitConverged
}

// showVersion prints what this build is, and what ExifTool it would
// drive. ExifTool's version is evidence about the identities this
// machine would produce, so a version report that omitted it would omit
// the half a bug report needs.
func (e *env) showVersion() int {
	e.out.printf("stampla %s\n", e.version)
	version, err := exiftoolVersion()
	if err != nil {
		e.out.printf("exiftool: %v\n", err)
		return finding.ExitConverged
	}
	e.out.printf("exiftool %s\n", version)
	return finding.ExitConverged
}

// exiftoolVersion asks the installed ExifTool what it is. A missing one
// is answered by exif.Available, whose error carries this platform's
// install hint.
func exiftoolVersion() (string, error) {
	exe, err := exec.LookPath("exiftool")
	if err != nil {
		return "", exif.Available()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stdout, err := exec.CommandContext(ctx, exe, "-ver").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(stdout)), nil
}

// usageExit reports a malformed command line: what was wrong, then the
// verb's own usage, both on stderr.
func (e *env) usageExit(verb string, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		if text, ok := usageFor(verb); ok {
			e.out.text(text)
		}
		return finding.ExitConverged
	}
	e.errOut.printf("stampla %s: %v\n\n", verb, err)
	if text, ok := usageFor(verb); ok {
		e.errOut.text(text)
	}
	return finding.ExitUsage
}

// refuse reports a run that cannot start, or trouble that ends one. It
// is the interface's whole vocabulary for exit 2: every refusal names
// its reason, and there is one place that prints them.
func (e *env) refuse(verb string, err error) int {
	e.errOut.printf("stampla %s: %v\n", verb, err)
	return finding.ExitAlarm
}

// worse is the run's exit code so far and one more: alarms dominate
// pending findings, which dominate a clean run.
func worse(a, b int) int {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func rank(code int) int {
	switch code {
	case finding.ExitConverged:
		return 0
	case finding.ExitPending:
		return 1
	default:
		return 2
	}
}

// out writes one of the interface's streams.
//
// Every write is deliberately unchecked: a report that cannot reach its
// pipe is not a reason to fail a run whose files have already landed,
// and there is nowhere left to report the failure to anyway.
type out struct{ w io.Writer }

func (o *out) printf(format string, args ...any) { _, _ = fmt.Fprintf(o.w, format, args...) }
func (o *out) line(text string)                  { _, _ = io.WriteString(o.w, text+"\n") }
func (o *out) text(text string)                  { _, _ = io.WriteString(o.w, text) }

// isTerminal reports whether a stream is a character device, which is
// the only question color and progress ask of one. Anything that is not
// an *os.File is not a terminal — which is what makes a test's buffer
// behave exactly like a pipe.
func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
