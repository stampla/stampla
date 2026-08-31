package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stampla/stampla/internal/layout"
)

// Apply executes a plan, one group at a time, in plan order.
//
// A group either fully lands or is put back the way it was and reported;
// either way the groups after it still run, because one unreadable file
// must never cost a card the rest of its import. Nothing here consults
// the world for a second opinion: the plan decided, the no-clobber
// primitives refuse anything the world changed in the meantime, and a
// re-run re-plans against what it then finds.
//
// Apply refuses a plan built for a verify mode. There is no dry-run
// flag: a dry run is BuildPlan without this call.
func Apply(plan *Plan, opts ApplyOptions) (*Result, error) {
	if plan == nil {
		return nil, errors.New("engine: no plan to apply")
	}
	if !plan.Mode.mutating() {
		return nil, fmt.Errorf("engine: %s: %w", plan.Mode, ErrReadOnlyMode)
	}
	root, err := os.OpenRoot(plan.Dest)
	if err != nil {
		return nil, fmt.Errorf("engine: %s: %w", plan.Dest, err)
	}
	defer func() { _ = root.Close() }()

	a := &applier{plan: plan, opts: opts, root: root, result: &Result{}}
	for i, group := range plan.Groups {
		opts.Progress.emit(PhaseApply, i, len(plan.Groups), firstPath(group))
		a.group(group)
	}
	opts.Progress.emit(PhaseApply, len(plan.Groups), len(plan.Groups), "")
	a.declare()
	return a.result, nil
}

// applier carries one Apply's state.
type applier struct {
	plan   *Plan
	opts   ApplyOptions
	root   *os.Root
	result *Result
	// copied says whether the group in flight took the copy path, which
	// is what reverting and source deletion both have to know.
	copied bool
}

func firstPath(group GroupPlan) string {
	if len(group.Actions) == 0 {
		return ""
	}
	return group.Actions[0].Old
}

// group applies one group, all or nothing.
func (a *applier) group(group GroupPlan) {
	work := make([]Action, 0, len(group.Actions))
	for _, action := range group.Actions {
		if action.Verb != VerbNone {
			work = append(work, action)
		}
	}
	if group.Refused || len(work) == 0 {
		a.result.Skipped = append(a.result.Skipped, group.Key)
		return
	}

	done, err := a.run(work)
	if err != nil {
		reverted := a.revert(done)
		a.result.Failed = append(a.result.Failed, Failure{
			Key: group.Key, Path: err.path, Err: err.err, Reverted: reverted,
		})
		return
	}

	// The receipt is written after the group has landed and is fsynced
	// before the next group starts, so the receipts of an interrupted
	// run name exactly the groups that completed — no more, and no
	// fewer. It is written before a cross-filesystem move deletes its
	// sources, deliberately: the receipt is the only record of what a
	// file used to be called, and a crash between the copy and the
	// record would lose the very thing the record exists for.
	if err := a.record(work); err != nil {
		a.result.Failed = append(a.result.Failed, Failure{
			Key: group.Key, Path: a.receiptPath(), Err: err, Reverted: false,
		})
	}
	a.result.Applied = append(a.result.Applied, group.Key)
	a.result.Members += len(work)
	if leftover := a.drop(group, work); leftover != nil {
		a.result.Failed = append(a.result.Failed, *leftover)
	}
}

// memberError is a failure attributed to the member that caused it.
type memberError struct {
	path string
	err  error
}

// run performs one group's work and returns what it did, so a failure
// can be undone.
func (a *applier) run(work []Action) ([]Action, *memberError) {
	if a.plan.Mode == Move && !a.opts.ForceCrossVolume {
		a.copied = false
		done, err := a.rename(work)
		if err == nil || !crossDevice(err.err) {
			return done, err
		}
		// The source is on another filesystem, where a rename cannot
		// reach. Put back whatever was renamed and do the group again as
		// a verified copy: that path deletes no source until every
		// member of the group has been read back at its destination.
		if !a.revert(done) {
			return done, err
		}
	}
	a.copied = true
	return a.copy(work)
}

// rename moves a group within one filesystem.
func (a *applier) rename(work []Action) ([]Action, *memberError) {
	done := make([]Action, 0, len(work))
	for _, action := range work {
		if action.Verb == VerbUnlink {
			if err := a.unlink(action); err != nil {
				return done, &memberError{path: action.Old, err: err}
			}
			done = append(done, action)
			continue
		}
		if err := a.prepare(action.New); err != nil {
			return done, &memberError{path: action.Old, err: err}
		}
		if err := claimRename(absPath(action.Old), action.New); err != nil {
			return done, &memberError{path: action.Old, err: err}
		}
		syncDir(filepath.Dir(action.New))
		done = append(done, action)
	}
	return done, nil
}

// copy copies a group in, verifying every landed file, and only then
// removes the sources a move would remove.
//
// The order is the promise: a source is deleted after its copy has been
// read back and matched, never before, and not until every member of the
// group has passed — a master deleted while its sidecar's copy failed
// would be a group torn in half.
func (a *applier) copy(work []Action) ([]Action, *memberError) {
	done := make([]Action, 0, len(work))
	for i, action := range work {
		if action.Verb == VerbUnlink {
			if err := a.unlink(action); err != nil {
				return done, &memberError{path: action.Old, err: err}
			}
			done = append(done, action)
			continue
		}
		if err := a.prepare(action.New); err != nil {
			return done, &memberError{path: action.Old, err: err}
		}
		a.opts.Progress.emit(PhaseVerify, i, len(work), action.Old)
		if err := copyInto(absPath(action.Old), action.New); err != nil {
			return done, &memberError{path: action.Old, err: err}
		}
		done = append(done, action)
	}
	a.opts.Progress.emit(PhaseVerify, len(work), len(work), "")
	return done, nil
}

// drop removes the sources of a completed cross-filesystem move.
//
// It runs only after every member of the group has landed and been
// verified. A source that will not go is not a reason to undo the group:
// the archive holds verified copies, and deleting them to "restore" a
// state the user asked to leave would destroy the very thing that was
// just proven good. It is reported instead, with the archive left
// correct and the source left in place.
func (a *applier) drop(group GroupPlan, work []Action) *Failure {
	if a.plan.Mode != Move || !a.copied {
		return nil // a rename left no source behind to delete
	}
	for _, action := range work {
		if action.Verb == VerbUnlink {
			continue // its source is already gone; that is what unlink did
		}
		old := absPath(action.Old)
		if err := os.Remove(old); err != nil {
			return &Failure{
				Key: group.Key, Path: action.Old, Reverted: false,
				Err: fmt.Errorf("the verified copy is in place at %s, but the source"+
					" could not be removed: %w", action.New, err),
			}
		}
	}
	return nil
}

// unlink finishes an interrupted rename by dropping the leftover source
// link. It re-proves that the two names are one file first: without that
// proof this would be deleting a source, which nothing here may do.
func (a *applier) unlink(action Action) error {
	old := absPath(action.Old)
	if err := sameFile(old, action.New); err != nil {
		return fmt.Errorf("refusing to drop %s: %w", old, err)
	}
	if err := os.Remove(old); err != nil {
		return err
	}
	syncDir(filepath.Dir(old))
	return nil
}

// prepare makes a target's directory and proves it is inside the
// archive.
//
// Containment is checked before the directory is created and again
// after: a component of the path can be a symlink out of the tree, and a
// write that followed one would land outside everything the plan
// described. The directory itself is created through an os.Root bound to
// the destination, which refuses to traverse a link out of it however
// the name is spelled.
func (a *applier) prepare(target string) error {
	dir := filepath.Dir(target)
	if err := contained(a.plan.Dest, dir); err != nil {
		return err
	}
	rel, err := filepath.Rel(a.plan.Dest, dir)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, ErrEscapesRoot)
	}
	if rel != "." {
		if err := a.root.MkdirAll(rel, 0o755); err != nil {
			return err
		}
	}
	return contained(a.plan.Dest, target)
}

// revert puts a partly applied group back, most recent first, and
// reports whether it managed. A group that cannot be reverted is the one
// state worth shouting about: a master split from its sidecars, which
// the next run's re-plan is what heals.
func (a *applier) revert(done []Action) bool {
	ok := true
	for i := len(done) - 1; i >= 0; i-- {
		action := done[i]
		switch {
		case action.Verb == VerbUnlink:
			// Put the second name back. The file itself never moved.
			if err := os.Link(action.New, absPath(action.Old)); err != nil {
				ok = false
			}
		case a.copied:
			// The source was never touched, so undoing the group is
			// dropping the copies this run made — and only those.
			if err := os.Remove(action.New); err != nil && !errors.Is(err, os.ErrNotExist) {
				ok = false
			}
		default:
			if err := claimRename(action.New, absPath(action.Old)); err != nil {
				ok = false
			}
		}
	}
	return ok
}

// record appends this group's mutations to the receipt.
func (a *applier) record(work []Action) error {
	lines := make([]receiptLine, 0, len(work))
	for _, action := range work {
		lines = append(lines, receiptLine{
			verb: action.Verb,
			old:  absPath(action.Old),
			new:  action.New,
		})
	}
	path := a.receiptPath()
	if err := appendReceipt(path, lines); err != nil {
		return err
	}
	a.result.Receipt = path
	return nil
}

func (a *applier) receiptPath() string {
	return filepath.Join(a.plan.Dest, ReceiptName)
}

// declare records the layout that actually shaped the tree, so the next
// run — on this machine or on whatever machine the disk is carried to —
// resolves the same one instead of falling back to a default that might
// have moved on.
//
// An existing marker is never rewritten: it is the user's declaration,
// and this run converged to it rather than the other way round.
func (a *applier) declare() {
	if a.result.Members == 0 || !a.layoutShaped() {
		return
	}
	marker, err := layout.ReadMarker(a.plan.Dest)
	if err != nil || marker != nil {
		return
	}
	marker = &layout.Marker{Dir: a.plan.Dest}
	marker.SetLayout(a.plan.Resolution.Pattern.String())
	if err := marker.Write(); err != nil {
		return
	}
	a.result.Marker = MarkerRecord{
		Written: true,
		Path:    marker.Path(),
		Pattern: a.plan.Resolution.Pattern.String(),
		Source:  a.plan.Resolution.Source,
	}
}

// layoutShaped reports whether the layout is the shape this run actually
// left behind: every file it wrote sits in the directory the layout
// renders for that file's capture time (or in a vendor sidecar
// subdirectory one level below it).
//
// This is what keeps an in-place rename under a fallback layout from
// declaring one. Names converge on an undeclared root while directories
// deliberately do not, so recording the fallback would tell the next run
// to reorganize a tree nobody asked it to touch — the exact thing a
// fallback layout is not allowed to do.
func (a *applier) layoutShaped() bool {
	applied := make(map[string]bool, len(a.result.Applied))
	for _, key := range a.result.Applied {
		applied[key] = true
	}
	for _, group := range a.plan.Groups {
		if !applied[group.Key] {
			continue
		}
		home := filepath.Join(a.plan.Dest,
			filepath.FromSlash(a.plan.Resolution.Pattern.Dir(group.Identity.Time)))
		for _, action := range group.Actions {
			if action.Verb == VerbNone {
				continue
			}
			dir := filepath.Dir(action.New)
			if dir != home && filepath.Dir(dir) != home {
				return false
			}
		}
	}
	return true
}
