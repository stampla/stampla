package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/layout"
)

// The mass-reorganization tripwire's two halves. Both have to trip: a
// hundred files is a large rename in a small archive and nothing at all
// in a large one, and half an archive is a reorganization whether it is
// six files or six thousand — but six files is not worth stopping for.
const (
	massFiles    = 100
	massFraction = 0.5
)

// damNamed is how many artifacts a prompt lists before it counts the
// rest. A confirmation is evidence, not an inventory.
const damNamed = 5

// Refusals that end a run at the confirmation exit code.
var (
	// errDeclined is an answer that was not yes.
	errDeclined = errors.New("declined")
	// errNoTerminal is no way to ask at all.
	errNoTerminal = errors.New("there is no terminal to ask on")
)

// tripwire is one confirmation: which predicate fired, and why, in the
// words the prompt puts to the person.
type tripwire struct {
	name   string
	reason string
}

// confirmInput is everything the predicates read. It is a value rather
// than the run's own state so that each predicate is a pure function of
// a plan and can be tested over a fabricated one.
type confirmInput struct {
	mode engine.Mode
	plan *engine.Plan
	// layout is the --layout flag: nil when it was not given.
	layout *string
	// removable is the volume root a source sits on when that volume
	// looks like removable media, and empty when none does.
	removable string
}

// tripwires evaluates every confirmation predicate, in a fixed order.
//
// All of them are evaluated before any of them is asked: a person
// deciding whether to let a run happen should see every reason it is
// unusual at once, not discover the second one after answering the
// first. A confirmation never changes the plan — it gates it.
func tripwires(in confirmInput) []tripwire {
	var wires []tripwire
	for _, predicate := range []func(confirmInput) (tripwire, bool){
		layoutOverride, massReorganization, damBeside, removableSourceWire,
	} {
		if wire, fired := predicate(in); fired {
			wires = append(wires, wire)
		}
	}
	return wires
}

// layoutOverride fires when an explicit --layout contradicts what the
// destination itself declares.
//
// The flag has already won — that is the resolution order — so the
// question is not which layout to use but whether the user meant to
// overrule a declaration that travels with these photographs.
func layoutOverride(in confirmInput) (tripwire, bool) {
	if in.layout == nil {
		return tripwire{}, false
	}
	marker := in.plan.Resolution.Marker
	if marker == nil || !marker.HasLayout() {
		return tripwire{}, false
	}
	declared := marker.Layout
	if parsed, err := layout.ParsePattern(declared); err == nil {
		declared = parsed.String()
	}
	asked := in.plan.Resolution.Pattern.String()
	if declared == asked {
		return tripwire{}, false
	}
	return tripwire{
		name: "layout",
		reason: fmt.Sprintf(
			"--layout %s contradicts %s, which declares %s = %q. The flag wins for this"+
				" run and the marker is left exactly as it is, so the next run without"+
				" the flag will file these photographs somewhere else.",
			quotePattern(asked), marker.Path(), layout.KeyLayout, declared),
	}, true
}

// massReorganization fires when a run would move most of an archive.
//
// This is the shape of a mistyped --layout on a large collection: every
// file is where it belongs under one declaration and nowhere near it
// under another, and the plan is correct, enormous, and not what anybody
// wanted.
func massReorganization(in confirmInput) (tripwire, bool) {
	fraction := in.plan.TouchedFraction()
	if in.plan.Touched <= massFiles || fraction <= massFraction {
		return tripwire{}, false
	}
	return tripwire{
		name: "reorganize",
		reason: fmt.Sprintf(
			"this would rename or relocate %d of the %d files already under %s (%.0f%%).",
			in.plan.Touched, in.plan.UnderRoot, in.plan.Dest, fraction*100),
	}, true
}

// damBeside fires when a digital asset manager's catalog sits beside the
// destination of an mv.
//
// A catalog tracks photographs by path, so moving them behind its back
// leaves it pointing at names that no longer exist. This is a question
// rather than a refusal because the catalog may well be an old one, or
// belong to files this run does not touch; the marker's dam key is the
// answer that settles it for good.
func damBeside(in confirmInput) (tripwire, bool) {
	if in.mode != engine.Move || len(in.plan.DAMArtifacts) == 0 {
		return tripwire{}, false
	}
	named := in.plan.DAMArtifacts
	suffix := ""
	if len(named) > damNamed {
		suffix = fmt.Sprintf(" and %d more", len(named)-damNamed)
		named = named[:damNamed]
	}
	return tripwire{
		name: "dam",
		reason: fmt.Sprintf(
			"a digital asset manager's files sit here: %s%s. A catalog tracks photographs"+
				" by path, so moving them behind its back orphans its entries. Recording"+
				" %s = %q in %s refuses these moves for good.",
			strings.Join(named, ", "), suffix,
			layout.KeyDAM, "lrc", markerPath(in.plan.Dest)),
	}, true
}

// removableSourceWire fires when an mv would empty removable media.
//
// mv deletes a source only after its copy has been re-read and verified
// at the destination, so nothing is at risk in the moment. What changes
// is afterwards: the card stops being the second copy of these
// photographs, and it stops being one before anybody has deliberately
// decided that the archive is the copy they trust.
func removableSourceWire(in confirmInput) (tripwire, bool) {
	if in.mode != engine.Move || in.removable == "" {
		return tripwire{}, false
	}
	return tripwire{
		name: "removable",
		reason: fmt.Sprintf(
			"%s looks like removable media. mv deletes each source only after its copy"+
				" has been re-read and verified in the archive, but the card then stops"+
				" being the last-resort backup — until you format it, it is the only"+
				" other copy of these photographs.",
			in.removable),
	}, true
}

// markerPath is where a destination's declaration lives, named in a
// prompt that suggests writing one.
func markerPath(dest string) string { return filepath.Join(dest, layout.MarkerName) }

// confirm puts every question to the person at the terminal, in order,
// and stops at the first answer that is not yes.
func (e *env) confirm(wires []tripwire) error {
	for _, wire := range wires {
		e.errOut.line("")
		e.errOut.line(wire.reason)
		answered, err := e.ask()
		if err != nil {
			return fmt.Errorf("%w, and this run is unusual enough to ask about;"+
				" pass -y to proceed without asking", err)
		}
		if !answered {
			return errDeclined
		}
	}
	return nil
}

// ask puts one question and reads its answer.
//
// The question goes to stderr, where every other thing the interface
// says about itself goes, and the answer comes from the controlling
// terminal — never from stdin, which may be the --stdin file list and
// would answer with a path. Anything that is not y or Y is no: a
// confirmation that defaults to yes is not a confirmation.
func (e *env) ask() (bool, error) {
	if e.answers == nil {
		tty, err := e.terminal()
		if err != nil {
			return false, fmt.Errorf("%w (%v)", errNoTerminal, err)
		}
		e.tty = tty
		e.answers = bufio.NewReader(tty)
	}
	e.errOut.text("proceed? [y/N] ")
	line, err := e.answers.ReadString('\n')
	if err != nil && line == "" {
		// End of input is an answer, and it is not yes. The newline is
		// this side's, since nobody typed one.
		e.errOut.line("")
		return false, nil
	}
	answer := strings.TrimSpace(line)
	return answer == "y" || answer == "Y", nil
}

func (e *env) closeTerminal() {
	if e.tty != nil {
		_ = e.tty.Close()
		e.tty, e.answers = nil, nil
	}
}
