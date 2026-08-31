package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/exif"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
)

// mutate runs cp or mv: the two verbs that act.
//
// The guardrails run in one fixed order — the shape of the command line,
// then the destination, then the layout, then the neighbourhood, then
// ExifTool — so that the first thing wrong with a command is the thing
// the user is told about, and nothing is started before all of it holds.
func (e *env) mutate(mode engine.Mode, args []string) int {
	verb := mode.String()
	opts, err := parseFlags(verb, args)
	if err != nil {
		return e.usageExit(verb, err)
	}
	sources, dest, err := opts.inputs(verb, e.stdin)
	if err != nil {
		return e.usageExit(verb, err)
	}
	if err := destDir(verb, dest); err != nil {
		return e.refuse(verb, err)
	}
	dest, err = absDest(dest)
	if err != nil {
		return e.refuse(verb, err)
	}
	res, err := layout.ResolveFlag(dest, opts.layoutFlag())
	if err != nil {
		return e.refuse(verb, layoutRefusal(err))
	}
	if err := insideArchive(dest); err != nil {
		return e.refuse(verb, err)
	}
	// Asked before a pool is started, so that a machine without ExifTool
	// is told how to get one instead of watching processes fail.
	if err := exif.Available(); err != nil {
		return e.refuse(verb, err)
	}
	pool, err := exif.NewPool(opts.workers)
	if err != nil {
		return e.refuse(verb, err)
	}
	defer func() { _ = pool.Close() }()

	rep := e.reporter(opts)
	rep.head(mode, dest, false, res)

	var list io.Reader
	if opts.stdin {
		list = e.stdin
	}
	scan, err := scanner.Collect(sources, scanner.Options{
		Stdin: list, NulSep: opts.nulSep, StopAtRoots: true,
	})
	if err != nil {
		return e.abort(verb, rep, err)
	}
	plan, err := engine.BuildPlan(engine.Options{
		Mode: mode, Scan: scan, Dest: dest, Resolution: res,
		Pool: pool, Workers: opts.workers, Progress: rep.progress,
	})
	if err != nil {
		return e.abort(verb, rep, err)
	}

	a := archive{root: dest, mode: mode, res: res, plan: plan, skipped: scan.Skipped}
	a.notes = append(a.notes, markerNotes(res.Warnings)...)

	wires := tripwires(confirmInput{
		mode:      mode,
		plan:      plan,
		layout:    opts.layoutFlag(),
		removable: removableSource(scan, dest),
	})
	switch {
	case opts.dryRun:
		// A preview changes nothing, so there is nothing to confirm — but
		// what the real run would stop to ask is part of the preview.
		for _, wire := range wires {
			a.notes = append(a.notes, "would ask before applying: "+wire.reason)
		}
	case opts.yes:
	default:
		if err := e.confirm(wires); err != nil {
			// Declined and refused are one code: both mean the plan was
			// not applied because a question was not answered yes. The
			// plan is still reported — what would have happened is the
			// most useful thing to know at that point.
			a.unwritten = "declined: nothing was written"
			rep.body(a)
			rep.tail(outcome{exit: finding.ExitDeclined})
			e.errOut.printf("stampla %s: %v\n", verb, err)
			return finding.ExitDeclined
		}
	}

	if opts.dryRun {
		a.unwritten = fmt.Sprintf("dry run: nothing was written (%s would be)",
			count(plan.Mutations(), "file"))
	} else {
		result, err := engine.Apply(plan, engine.ApplyOptions{Progress: rep.progress})
		if err != nil {
			return e.abort(verb, rep, err)
		}
		a.result = result
	}
	// Asked of the finished run, not of the plan: a run that declared the
	// destination on its way out has nothing left to suggest.
	a.notes = append(a.notes, undeclaredHint(a)...)

	code := exitCode(a)
	rep.body(a)
	rep.tail(outcomeOf(code, a.result))
	return code
}

// verify runs the read-only verb, in whichever of its two modes the
// argument count asked for.
func (e *env) verify(args []string) int {
	opts, err := parseFlags(verbVerify, args)
	if err != nil {
		return e.usageExit(verbVerify, err)
	}
	src, dest, err := opts.destArgs()
	if err != nil {
		return e.usageExit(verbVerify, err)
	}
	if err := destDir(verbVerify, dest); err != nil {
		return e.refuse(verbVerify, err)
	}
	dest, err = absDest(dest)
	if err != nil {
		return e.refuse(verbVerify, err)
	}
	if err := exif.Available(); err != nil {
		return e.refuse(verbVerify, err)
	}
	pool, err := exif.NewPool(opts.workers)
	if err != nil {
		return e.refuse(verbVerify, err)
	}
	defer func() { _ = pool.Close() }()

	rep := e.reporter(opts)
	if src != "" {
		return e.membership(rep, pool, opts, src, dest)
	}
	return e.selfVerify(rep, pool, opts, dest)
}

// membership answers "is this source accounted for in that archive".
//
// The source is scanned whole, nested roots and all: exit 0 means every
// file in it is present at its place in the destination, and a scan that
// stopped somewhere would answer for fewer files than the question asked
// about. That is the answer a card is formatted on.
func (e *env) membership(rep reporter, pool *exif.Pool, opts *options, src, dest string) int {
	res, err := layout.Resolve(dest, "")
	if err != nil {
		return e.refuse(verbVerify, layoutRefusal(err))
	}
	rep.head(engine.VerifyMembership, dest, false, res)

	scan, err := scanner.Collect([]string{src}, scanner.Options{})
	if err != nil {
		return e.abort(verbVerify, rep, err)
	}
	plan, err := engine.BuildPlan(engine.Options{
		Mode: engine.VerifyMembership, Scan: scan, Dest: dest, Resolution: res,
		Pool: pool, Workers: opts.workers, Progress: rep.progress,
	})
	if err != nil {
		return e.abort(verbVerify, rep, err)
	}
	a := archive{root: dest, mode: engine.VerifyMembership, res: res, plan: plan, skipped: scan.Skipped}
	a.notes = append(a.notes, markerNotes(res.Warnings)...)
	rep.body(a)

	code := exitCode(a)
	rep.tail(outcome{exit: code})
	return code
}

// selfVerify checks an archive against itself, and then every archive
// nested inside it under that archive's own declaration.
//
// Each scan stops where another archive begins and that archive is
// verified next, on its own terms, so every file is classified exactly
// once by the layout that governs it. Verifying a nested tree under its
// parent's layout would report a correctly filed photograph as
// misplaced, which is a report about the wrong archive.
func (e *env) selfVerify(rep reporter, pool *exif.Pool, opts *options, dest string) int {
	code := finding.ExitConverged
	roots := []string{dest}
	seen := map[string]bool{dest: true}
	// roots grows as nested archives are met, which is what makes this a
	// breadth-first descent rather than a loop over a fixed list.
	for i := 0; i < len(roots); i++ {
		root := roots[i]
		res, err := layout.Resolve(root, "")
		if err != nil {
			e.errOut.printf("stampla %s: %v\n", verbVerify, layoutRefusal(err))
			code = worse(code, finding.ExitAlarm)
			continue
		}
		rep.head(engine.VerifySelf, root, i > 0, res)

		scan, err := scanner.Collect([]string{root}, scanner.Options{StopAtRoots: true})
		if err != nil {
			return e.abort(verbVerify, rep, err)
		}
		plan, err := engine.BuildPlan(engine.Options{
			Mode: engine.VerifySelf, Scan: scan, Dest: root, Resolution: res,
			Pool: pool, Workers: opts.workers, Progress: rep.progress,
		})
		if err != nil {
			return e.abort(verbVerify, rep, err)
		}
		a := archive{
			root: root, nested: i > 0, mode: engine.VerifySelf,
			res: res, plan: plan, skipped: scan.Skipped,
		}
		a.notes = append(a.notes, markerNotes(res.Warnings)...)
		rep.body(a)
		code = worse(code, exitCode(a))

		for _, nested := range scan.NestedRoots {
			abs, err := absDest(nested)
			if err != nil || seen[abs] {
				continue
			}
			seen[abs] = true
			roots = append(roots, abs)
		}
	}
	rep.tail(outcome{exit: code})
	return code
}

// abort ends a run that cannot continue, after the report has already
// begun: the stream still gets its result envelope, so a machine reader
// never has to tell a failed run from a truncated one.
func (e *env) abort(verb string, rep reporter, err error) int {
	rep.tail(outcome{exit: finding.ExitAlarm})
	return e.refuse(verb, err)
}

// outcomeOf is what a finished mutation has to say for itself.
func outcomeOf(code int, result *engine.Result) outcome {
	o := outcome{exit: code}
	if result == nil {
		return o
	}
	o.applied = result.Members
	o.failed = len(result.Failed)
	o.receipt = result.Receipt
	if result.Marker.Written {
		o.marker = result.Marker.Path
	}
	return o
}

// layoutRefusal explains a layout the chain would not resolve. A
// container is the one refusal worth rewording: its cause is a directory
// doing exactly what it was told to do, so the message says what to do
// instead rather than only what went wrong.
func layoutRefusal(err error) error {
	var container *layout.ContainerError
	if errors.As(err, &container) {
		return fmt.Errorf(
			"%s declares %s and no layout of its own, so it is a container that holds"+
				" archives rather than photographs; name one of the archives beneath"+
				" it instead, or create one",
			container.Marker.Path(), layout.KeyLayoutForChildren)
	}
	return err
}

// insideArchive refuses a mutation whose destination belongs to an
// archive rooted above it.
//
// Files converged into a subdirectory of somebody else's archive are
// filed under a layout that archive never declared, where its own next
// run will report them as misplaced. The archive root is the unit a
// declaration governs, so it is the unit a destination has to be.
//
// verify is deliberately allowed to do this: checking a subtree is a
// legitimate read-only question, and answering it files nothing.
func insideArchive(dest string) error {
	parent := filepath.Dir(dest)
	if parent == dest {
		return nil
	}
	marker, err := layout.NearestRoot(parent)
	if err != nil {
		return err
	}
	if marker == nil {
		return nil
	}
	return fmt.Errorf(
		"destination is inside an archive rooted at %s — use %s as the destination"+
			" (%s declares its layout, and it is the archive that owns this directory)",
		marker.Dir, marker.Dir, marker.Path())
}

// undeclaredHint says why a converged archive did not move anything.
//
// A fallback layout may place files entering a root and may never
// reorganize one, so an in-place run under an undeclared layout renames
// and stops there. Without the hint that reads like a run that half
// worked.
func undeclaredHint(a archive) []string {
	if a.res.Declared || a.plan.UnderRoot == 0 {
		return nil
	}
	if a.result != nil && a.result.Marker.Written {
		return nil // this run declared it, and the next one will read that
	}
	return []string{fmt.Sprintf(
		"hint: %s declares no layout of its own (this run used %s, %s), so files already"+
			" under it converged by name only. Pass --layout, or write %s = %q in %s, to"+
			" file them by date as well.",
		a.root, quotePattern(a.res.Pattern.String()), provenanceText(a.res),
		layout.KeyLayout, a.res.Pattern.String(), markerPath(a.root))}
}

// markerNotes labels what a marker file said that this version does not
// understand. Nothing is ever dropped because of a warning — the lines
// survive the next rewrite verbatim — so the report says so and carries
// on.
func markerNotes(warnings []string) []string {
	notes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		notes = append(notes, "warning: "+warning)
	}
	return notes
}

// reporter builds the one this run speaks through.
func (e *env) reporter(opts *options) reporter {
	if opts.porcelain {
		return newPorcelain(e.out)
	}
	return newHuman(e.out, e.errOut,
		palette{on: colorOn(opts.color, e.stdoutTTY, os.Getenv("NO_COLOR"))},
		e.stderrTTY)
}
