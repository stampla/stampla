package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The --color modes.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
)

// options is one parsed command line. One struct serves both verbs:
// what a verb accepts is decided by which flags it registers, not by
// which fields exist, so a flag can never be read where it was never
// offered.
type options struct {
	dryRun    bool
	yes       bool
	layout    string
	layoutSet bool
	stdin     bool
	nulSep    bool
	porcelain bool
	color     string
	workers   int
	args      []string
}

// parseFlags registers what verb accepts and parses args.
//
// The standard library's flag package takes one dash or two for every
// name, which is why -n and --dry-run are one flag registered twice
// rather than a parser of its own.
func parseFlags(verb string, args []string) (*options, error) {
	o := &options{color: colorAuto}
	fs := flag.NewFlagSet("stampla "+verb, flag.ContinueOnError)
	// The usage text belongs to this package, and where it is printed is
	// the caller's decision: flag's own output would put -h on stderr.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	if verb != verbVerify {
		fs.BoolVar(&o.dryRun, "n", false, "")
		fs.BoolVar(&o.dryRun, "dry-run", false, "")
		fs.BoolVar(&o.yes, "y", false, "")
		fs.BoolVar(&o.yes, "yes", false, "")
		fs.StringVar(&o.layout, "layout", "", "")
		fs.BoolVar(&o.stdin, "stdin", false, "")
		fs.BoolVar(&o.nulSep, "z", false, "")
	}
	fs.BoolVar(&o.porcelain, "porcelain", false, "")
	fs.StringVar(&o.color, "color", colorAuto, "")
	fs.IntVar(&o.workers, "workers", 0, "")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// An absent --layout and --layout "" are different questions to the
	// layout chain, and the flag set is the only thing that knows which
	// one was asked.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "layout" {
			o.layoutSet = true
		}
	})
	o.args = fs.Args()
	if err := o.check(); err != nil {
		return nil, err
	}
	return o, nil
}

// check refuses flag combinations that parse but mean nothing.
func (o *options) check() error {
	switch o.color {
	case colorAuto, colorAlways, colorNever:
	default:
		return fmt.Errorf("--color takes %s, %s or %s, not %q",
			colorAuto, colorAlways, colorNever, o.color)
	}
	if o.workers < 0 {
		return fmt.Errorf("--workers %d: a worker count cannot be negative", o.workers)
	}
	if o.nulSep && !o.stdin {
		return fmt.Errorf("-z describes the --stdin list; it means nothing without it")
	}
	return nil
}

// layoutFlag is the --layout flag as the layout chain takes it: nil when
// it was not given, and a pointer to "" when it was given as the flat
// layout.
func (o *options) layoutFlag() *string {
	if !o.layoutSet {
		return nil
	}
	return &o.layout
}

// inputs works out what a mutation verb was pointed at: the sources and
// the destination.
//
// The last positional is the destination, so a forgotten one is a glob
// that ate it. That case is caught here only when the count is wrong;
// when the count is right and the last argument is simply not a
// directory, destDir is what names it.
func (o *options) inputs(verb string, stdin io.Reader) (sources []string, dest string, err error) {
	if o.stdin {
		if isTerminal(stdin) {
			return nil, "", fmt.Errorf(
				"--stdin reads the file list from standard input, and standard input is a"+
					" terminal; pipe a list in (find /card -type f -print0 | stampla %s"+
					" --stdin -z <dest>) or name the inputs as arguments", verb)
		}
		if len(o.args) != 1 {
			return nil, "", fmt.Errorf(
				"--stdin takes the destination and nothing else, and %d arguments were given;"+
					" the file list comes from standard input", len(o.args))
		}
		return nil, o.args[0], nil
	}
	switch len(o.args) {
	case 0:
		return nil, "", fmt.Errorf("no inputs and no destination")
	case 1:
		return nil, "", fmt.Errorf(
			"no destination: %s takes the files to converge and then the archive to"+
				" converge them into (stampla %s <inputs...> <dest>)", verb, verb)
	default:
		return o.args[:len(o.args)-1], o.args[len(o.args)-1], nil
	}
}

// destArgs works out what a verify was pointed at: an archive, or a
// source and the archive it should be accounted for in.
func (o *options) destArgs() (src, dest string, err error) {
	switch len(o.args) {
	case 1:
		return "", o.args[0], nil
	case 2:
		return o.args[0], o.args[1], nil
	case 0:
		return "", "", fmt.Errorf("no archive to verify")
	default:
		return "", "", fmt.Errorf(
			"verify takes an archive, or a source and an archive, and %d arguments were"+
				" given", len(o.args))
	}
}

// destDir refuses a destination that is not an existing directory.
//
// stampla never creates an archive root: a mistyped path that became a
// directory would be an archive nobody meant to start, and the files
// would be filed somewhere their owner would not look. The message for
// a path that exists but is not a directory names it as the last
// argument, because the usual way to arrive here is a glob that expanded
// over the destination.
func destDir(verb, dest string) error {
	info, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf(
			"%s: %w; the destination must be an existing directory, and stampla never"+
				" creates one", dest, err)
	}
	if info.IsDir() {
		return nil
	}
	return fmt.Errorf(
		"%s is not a directory, and the last argument is the destination;"+
			" a glob such as *.jpg passes every match as an argument, so the archive"+
			" has to be named after it (stampla %s <inputs...> <dest>)", dest, verb)
}

// absDest is the destination as every layer beneath reports it: cleaned
// and absolute, so a report quotes one spelling of the archive whatever
// directory the command was run from.
func absDest(dest string) (string, error) {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("%s: %w", dest, err)
	}
	return filepath.Clean(abs), nil
}

// quotePattern renders a layout pattern for a message. The flat layout
// is the empty string, which quoted back as nothing at all would read as
// a missing word.
func quotePattern(pattern string) string {
	if pattern == "" {
		return `"" (flat)`
	}
	return pattern
}
