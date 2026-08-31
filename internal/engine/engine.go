package engine

import (
	"errors"
	"fmt"

	"github.com/stampla/stampla/internal/exif"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/identity"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
)

// Mode is which entry point to the one operation a run is.
type Mode int

const (
	// Copy converges by copying files in. Sources are never modified
	// and never deleted.
	Copy Mode = iota
	// Move converges by moving files in, or by renaming and relocating
	// files that already sit under the destination.
	Move
	// VerifyMembership answers "is this source accounted for in this
	// archive": every source group's expected path under the
	// destination's layout is derived and checked. Nothing is hashed at
	// the destination and nothing is ever written.
	VerifyMembership
	// VerifySelf classifies everything under the destination against
	// its own recomputed identities. This is the deep check: every
	// examined file's content is read.
	VerifySelf
)

func (m Mode) String() string {
	switch m {
	case Copy:
		return "cp"
	case Move:
		return "mv"
	case VerifyMembership:
		return "verify-membership"
	case VerifySelf:
		return "verify-self"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// mutating reports whether the mode may write.
func (m Mode) mutating() bool { return m == Copy || m == Move }

// Phase names what a progress event is about.
type Phase string

const (
	// PhaseRead is the metadata batch: one ExifTool read of every
	// group master.
	PhaseRead Phase = "read"
	// PhaseHash is the whole-file digest of the masters ExifTool has no
	// payload hash for.
	PhaseHash Phase = "hash"
	// PhaseApply is the per-group mutation loop.
	PhaseApply Phase = "apply"
	// PhaseVerify is reading copied bytes back at their destination.
	PhaseVerify Phase = "verify"
)

// ProgressFunc is called as a run advances: the phase, how many units of
// it are done out of how many, and the path currently being worked on.
// A total of zero means the total is not yet known. It is called from
// the goroutine driving that phase and never concurrently with itself;
// it must not block for long, and this package never prints anything of
// its own.
type ProgressFunc func(phase Phase, done, total int, path string)

func (f ProgressFunc) emit(phase Phase, done, total int, path string) {
	if f != nil {
		f(phase, done, total, path)
	}
}

// Options is everything BuildPlan takes.
type Options struct {
	// Mode is which of the four entry points this run is.
	Mode Mode
	// Scan is the collected inputs. For Copy, Move and
	// VerifyMembership it is the sources; for VerifySelf it is the
	// destination's own tree.
	Scan *scanner.Scan
	// Dest is the destination root. It must be an existing directory.
	Dest string
	// Resolution is the layout that governs Dest, already resolved by
	// the caller so that the provenance in every report is the one the
	// caller will print.
	Resolution layout.Resolution
	// Pool reads metadata. It is required for every mode: an identity
	// is never derived from a filename.
	Pool *exif.Pool
	// Workers caps the whole-file hashing pool; zero asks for one per
	// CPU.
	Workers int
	// Progress, when set, is called as planning advances.
	Progress ProgressFunc
}

// Verb is what applying an action does.
type Verb string

const (
	// VerbNone is an action that mutates nothing.
	VerbNone Verb = ""
	// VerbCopy copies the source to the target and leaves the source
	// alone. It is the verb the receipt records for cp.
	VerbCopy Verb = "cp"
	// VerbMove puts the source at the target and removes it from where
	// it was — a rename on one volume, a verified copy followed by a
	// delete across two. It is the verb the receipt records for mv.
	VerbMove Verb = "mv"
	// VerbUnlink drops a source that is already hard-linked at its
	// target: the window between a rename's link claim and its unlink,
	// left behind by an interrupted run and finished here. The target
	// is provably the same file, so this deletes no information; the
	// receipt records it as the mv it completes.
	VerbUnlink Verb = "unlink"
)

// Action is what the plan has to say about one file.
type Action struct {
	// Class is the file's disposition; every examined file has exactly
	// one.
	Class finding.Class
	// Verb is what Apply would do. VerbNone for everything a verify
	// mode plans, for converged files, and for every refusal.
	Verb Verb
	// Old is the file as the scan encountered it.
	Old string
	// New is where it belongs: the target path for a mutation, the
	// expected path for a membership check, and empty when the class
	// implies no destination.
	New string
	// Detail is the one evidence sentence behind the class.
	Detail string
	// Implied is true for a member the scan pulled in because its group
	// was selected rather than because an input named it.
	Implied bool
}

// GroupPlan is what the plan has to say about one convergence group.
type GroupPlan struct {
	// Key is the scanner's group key.
	Key string
	// Class is the group's own disposition: the class of its master, or
	// the reason the whole group is refused.
	Class finding.Class
	// Master is the group's hash-carrying member, empty when the group
	// has none.
	Master string
	// Identity is the master's recomputed identity. It is the zero
	// value when the group could not be identified.
	Identity identity.Identity
	// Provenance is the evidence behind that identity: which tag dated
	// the file and which digest named it.
	Provenance identity.Provenance
	// Actions are the group's members, in the order to act in — master
	// first, then sidecars and derivatives by path.
	Actions []Action
	// Detail explains a group-level refusal.
	Detail string
	// Refused is true when nothing in this group may be applied: an
	// alarm, an unresolvable capture time, a conflict, a member outside
	// the group's home. Apply skips it.
	Refused bool
}

// Plan is one run's complete, deterministic decision. The same input
// state yields the same Plan, down to the order of every slice.
type Plan struct {
	// Mode is the mode the plan was built for; Apply refuses any other.
	Mode Mode
	// Dest is the destination root, absolute and cleaned.
	Dest string
	// Resolution is the layout that governed placement, carried so that
	// every report can state it and its provenance.
	Resolution layout.Resolution
	// Groups are the convergence groups in plan order.
	Groups []GroupPlan
	// Findings is the run's report: the scan's own troubles first, then
	// one finding per action and one per group-level refusal, in plan
	// order. finding.ExitCode over this slice is the run's exit code.
	Findings []finding.Finding
	// Counts is how many findings carry each class.
	Counts map[finding.Class]int
	// UnderRoot counts the examined files that already sit under Dest.
	UnderRoot int
	// Touched counts those of them the plan would rename or relocate.
	// With UnderRoot it is the mass-rename confirmation tripwire; this
	// package computes it and never acts on it.
	Touched int
	// DAMArtifacts are digital-asset-manager catalogs and sessions
	// found beside or directly inside Dest, sorted. Their presence is a
	// confirmation tripwire for the caller; detecting them changes
	// nothing here.
	DAMArtifacts []string
}

// TouchedFraction is the share of the files already under the
// destination that the plan would rename or relocate. It is zero when
// the plan touches nothing that was already there, which is the case for
// every import from outside the root.
func (p *Plan) TouchedFraction() float64 {
	if p.UnderRoot == 0 {
		return 0
	}
	return float64(p.Touched) / float64(p.UnderRoot)
}

// ExitCode is the run's exit code, derived from its findings.
func (p *Plan) ExitCode() int { return finding.ExitCode(p.Findings) }

// Alarms counts the findings that are evidence of damage.
func (p *Plan) Alarms() int {
	return p.Counts[finding.Corrupt] + p.Counts[finding.TimeDrift]
}

// Mutations counts the files Apply would write.
func (p *Plan) Mutations() int {
	n := 0
	for _, group := range p.Groups {
		for _, action := range group.Actions {
			if action.Verb != VerbNone {
				n++
			}
		}
	}
	return n
}

// ApplyOptions configures one Apply.
type ApplyOptions struct {
	// Progress, when set, is called as the apply loop advances.
	Progress ProgressFunc
	// ForceCrossVolume makes Move take the copy-verify-delete path even
	// where a rename would work. It exists so that path is testable
	// without a second filesystem, and nothing else should set it.
	ForceCrossVolume bool
}

// Failure is one group Apply could not land. Whatever the group had
// already done was reverted before this was recorded, so the group is in
// the state it started in.
type Failure struct {
	// Key is the group's key.
	Key string
	// Path is the member that failed.
	Path string
	// Err is why.
	Err error
	// Reverted is false when the revert itself failed, which is the one
	// case that leaves a group in a mixed state. Err then says so.
	Reverted bool
}

func (f Failure) String() string {
	return fmt.Sprintf("%s: %v", f.Key, f.Err)
}

// MarkerRecord says whether a completed run declared the destination an
// archive, and with what.
type MarkerRecord struct {
	// Written is true when this run created the marker.
	Written bool
	// Path is the marker file.
	Path string
	// Pattern is the layout it declares.
	Pattern string
	// Source is where that layout came from, ready to print.
	Source string
}

// Result is what one Apply did.
//
// A group appears in exactly one of Applied and Failed, with one
// exception: a cross-filesystem move whose copies all verified but whose
// source could not then be deleted is applied — the archive holds the
// files — and reported, because the source is still there.
//
// Failed is operational trouble rather than a finding: there is no
// class for "this could not be written", so nothing in Findings speaks
// for it and finding.ExitCode cannot see it. A caller reports a
// non-empty Failed at the alarm exit code (finding.ExitAlarm, 2), which
// is where the interface contract puts operational trouble, and reports
// it whatever the plan's own exit code was.
type Result struct {
	// Applied are the keys of the groups that fully landed, in plan
	// order.
	Applied []string
	// Landed are the actions that were actually performed, in the order
	// they were performed — the group order of the plan, and within
	// each group the order Apply acted in. It is the report of what
	// changed: one entry per mutated file, carrying the same old and
	// new paths the receipt recorded for it, in the same order. A run
	// that wrote nothing leaves it empty, so a preview and a refused
	// run both report no changes rather than an absence of information.
	Landed []Action
	// Skipped are the keys of the groups that had nothing to do or were
	// refused by the plan.
	Skipped []string
	// Failed are the groups that could not land.
	Failed []Failure
	// Members counts the files that landed. It is len(Landed).
	Members int
	// Receipt is the receipt file every landed member was recorded in,
	// empty when nothing was recorded.
	Receipt string
	// Marker records the destination marker this run wrote, if any.
	Marker MarkerRecord
}

// Errors a caller distinguishes.
var (
	// ErrDAMManaged refuses to converge into an archive whose masters
	// another tool renames.
	ErrDAMManaged = errors.New("destination is managed by a digital asset manager")
	// ErrNotDir refuses a destination that is not an existing
	// directory.
	ErrNotDir = errors.New("destination is not a directory")
	// ErrNoPool refuses a run with no way to read metadata. An identity
	// is never derived from a filename.
	ErrNoPool = errors.New("no exiftool pool")
	// ErrReadOnlyMode refuses to apply a plan built for a verify mode.
	ErrReadOnlyMode = errors.New("mode never mutates")
	// ErrEscapesRoot refuses a target that resolves outside the
	// destination root.
	ErrEscapesRoot = errors.New("target escapes the destination root")
	// ErrTargetExists refuses to overwrite. It is the failure every
	// no-clobber primitive reports.
	ErrTargetExists = errors.New("target already exists")
)

// DAMError refuses a destination whose marker hands master renaming to a
// digital asset manager. It names the way forward rather than only the
// refusal.
type DAMError struct {
	// DAM is the value of the marker's dam key.
	DAM string
	// Marker is the file that declared it.
	Marker string
}

func (e *DAMError) Error() string {
	return fmt.Sprintf("%s: %s declares dam = %q, so its masters are renamed by that"+
		" tool and never here; use --inject to write the computed names where it"+
		" can read them", ErrDAMManaged, e.Marker, e.DAM)
}

func (e *DAMError) Unwrap() error { return ErrDAMManaged }

// ExistsError is the no-clobber refusal: a target was occupied at the
// moment of the write.
type ExistsError struct {
	// Path is the target that was already there.
	Path string
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("%s: %s", ErrTargetExists, e.Path)
}

func (e *ExistsError) Unwrap() error { return ErrTargetExists }

// EscapeError is the containment refusal.
type EscapeError struct {
	// Path is the target that would have landed outside.
	Path string
	// Resolved is where it actually resolved to.
	Resolved string
	// Root is the destination root it had to stay under.
	Root string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s: %s resolves to %s, outside %s",
		ErrEscapesRoot, e.Path, e.Resolved, e.Root)
}

func (e *EscapeError) Unwrap() error { return ErrEscapesRoot }
