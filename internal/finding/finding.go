// Package finding defines the shared vocabulary of the convergence
// engine: the classes every examined file falls into, the finding
// record itself, and the mapping from a run's findings to its exit
// code. Every other package speaks in these terms; none redefines
// them.
package finding

// Class is the disposition of one file (or group member) in a run.
// Every examined file lands in exactly one class.
type Class string

const (
	// Converged means name, hash and location all match; nothing to do.
	Converged Class = "converged"
	// Misplaced means correct name, wrong directory per the declared layout.
	Misplaced Class = "misplaced"
	// Stale means recomputed identity differs on an editable format; rename.
	Stale Class = "stale"
	// Corrupt means payload hash differs on a write-once format. Alarm;
	// the file is never renamed — the old name is the only record of
	// what its identity used to be.
	Corrupt Class = "corrupt"
	// TimeDrift means capture time differs on a write-once format while the
	// payload hash is intact. Alarm; ImageDataHash does not cover
	// metadata, so this must not pass as an innocent date edit.
	TimeDrift Class = "time-drift"
	// Unresolvable means no capture time derivable from metadata.
	Unresolvable Class = "unresolvable"
	// Conflict means the target path is occupied by different content.
	Conflict Class = "conflict"
	// Missing means membership check only — not present at the expected
	// path in the destination archive.
	Missing Class = "missing"
	// Incoming means not yet in the destination; will be copied or moved in.
	Incoming Class = "incoming"
)

// Alarm reports whether the class is evidence of damage rather than
// ordinary pending work.
func (c Class) Alarm() bool { return c == Corrupt || c == TimeDrift }

// Pending reports whether the class represents actionable divergence
// (work a mutation verb would perform, or absence a copy would fill).
func (c Class) Pending() bool {
	switch c {
	case Misplaced, Stale, Conflict, Missing, Unresolvable, Incoming:
		return true
	}
	return false
}

// Finding is one classified observation, with the evidence needed to
// act on or explain it.
type Finding struct {
	Class Class
	// Path is the file the finding is about, as encountered.
	Path string
	// Old and New are the planned rename/move endpoints where the
	// class implies one (stale, misplaced, incoming), or the expected
	// path for missing.
	Old string
	New string
	// Detail is one evidence sentence: the tag that dated the file,
	// the hash that disagreed, the marker that declared the layout.
	Detail string
}

// Exit codes of the command-line interface.
const (
	ExitConverged = 0 // everything verified / nothing to do / applied cleanly
	ExitPending   = 1 // findings that need action
	ExitAlarm     = 2 // corrupt or time-drift findings (dominates pending)
	ExitDeclined  = 3 // a confirmation was declined or could not be asked
	ExitUsage     = 64
)

// ExitCode derives the run's exit code from its findings: alarms
// dominate pending work; a clean run is converged.
func ExitCode(findings []Finding) int {
	code := ExitConverged
	for _, f := range findings {
		if f.Class.Alarm() {
			return ExitAlarm
		}
		if f.Class.Pending() {
			code = ExitPending
		}
	}
	return code
}
