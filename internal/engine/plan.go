package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stampla/stampla/internal/exif"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/identity"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
)

// BuildPlan works out everything a run would do, and does none of it.
//
// It reads: metadata for every group master, the whole-file digest of
// the masters ExifTool has no payload hash for, the directory listings
// of the target directories, and the markers of the destination and its
// neighbourhood. It writes nothing at all — not a temporary file, not a
// directory, not lazily on first use — because a dry run is this
// function without Apply after it, and a preview that created something
// would not be a preview.
//
// Errors are returned only for conditions that make the whole run
// impossible: a destination that is not a directory, no way to read
// metadata, a destination another tool owns. Everything else is a
// finding, so that one unreadable file never costs a card its import.
func BuildPlan(opts Options) (*Plan, error) {
	if opts.Scan == nil {
		return nil, fmt.Errorf("engine: no scan to plan from")
	}
	if opts.Pool == nil {
		return nil, fmt.Errorf("engine: %w", ErrNoPool)
	}
	dest := absPath(opts.Dest)
	info, err := os.Stat(dest)
	if err != nil {
		return nil, fmt.Errorf("engine: %s: %w", opts.Dest, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("engine: %s: %w", opts.Dest, ErrNotDir)
	}

	p := &planner{
		opts:      opts,
		dest:      dest,
		entries:   make(map[string][]string),
		roots:     make(map[string]string),
		claimed:   make(map[string]claim),
		targets:   make(map[string]groupTargets),
		contained: make(map[string]error),
		plan: &Plan{
			Mode:       opts.Mode,
			Dest:       dest,
			Resolution: opts.Resolution,
			Counts:     make(map[finding.Class]int),
		},
	}
	if opts.Mode.mutating() {
		if err := p.refuseDAM(); err != nil {
			return nil, err
		}
	}
	p.plan.DAMArtifacts = damArtifacts(dest)
	p.plan.Findings = append(p.plan.Findings, opts.Scan.Errors...)

	p.identify()
	p.probeSubstitution()
	for _, group := range opts.Scan.Groups {
		p.planGroup(group)
	}
	p.collect()
	return p.plan, nil
}

// planner carries one BuildPlan's state.
type planner struct {
	opts Options
	dest string
	plan *Plan

	// identities holds one entry per group, keyed by scanner.Group.ID:
	// a still and a clip sharing a base name are two groups under one
	// key, and they have two identities.
	identities map[string]resolved
	// entries caches directory listings, so a target directory is read
	// once however many files land in it.
	entries map[string][]string
	// roots caches the nearest archive root of a directory.
	roots map[string]string
	// claimed records which target path each group took, so that two
	// sources deriving one name are settled rather than raced.
	claimed map[string]claim
	// occupants holds the payload digest of every occupied master
	// target, used only to say which kind of conflict an occupied name
	// is.
	occupants map[string]hashResult
	// wholes holds whole-file digests, of both sides of every content
	// comparison the plan has to make.
	wholes map[string]hashResult
	// seeded holds the whole-file digests identification already had to
	// compute, for the formats ExifTool isolates no payload in.
	seeded map[string]hashResult
	// targets holds where each group's members would land, worked out
	// once and read by every pass, keyed by scanner.Group.ID.
	targets map[string]groupTargets
	// contained caches the containment verdict of each target
	// directory.
	contained map[string]error
}

// resolved is a group master's computed identity, or why it has none.
type resolved struct {
	master scanner.Item
	id     identity.Identity
	prov   identity.Provenance
	// payload is the digest the identity was cut from: ExifTool's
	// image-data hash, or the whole file where the format has no payload
	// ExifTool isolates. It names the file. It does not say whether one
	// file can stand in for another — see substitutable.
	payload string
	err     error
}

// memberTarget is one member of a group and where it would land.
type memberTarget struct {
	item   scanner.Item
	source string
	target string
}

// groupTargets is where a whole group would land.
//
// It is worked out once and read by both the pass that decides which
// files have to be digested and the pass that classifies them, so the
// two can never disagree about where a file was going — which would mean
// comparing the contents of the wrong pair.
type groupTargets struct {
	entry     resolved
	base      string
	masterDir string
	dir       string
	entering  bool
	members   []memberTarget
	// stray is a member sitting outside its group's home directory,
	// which no relocation can place without guessing; strayed says one
	// was found.
	stray   scanner.Item
	strayed bool
}

// claim records the group that took a set of target paths first, with
// what it would put at each of them.
type claim struct {
	key     string
	master  string
	payload string
	// digests maps each claimed target to the whole-file digest of the
	// source that claimed it.
	digests map[string]string
}

// refuseDAM stops a mutation into an archive whose masters another tool
// renames. It is a refusal rather than a confirmation: renaming behind a
// DAM's back orphans its catalog entries, and no answer to a prompt
// makes that safe.
func (p *planner) refuseDAM() error {
	marker := p.opts.Resolution.Marker
	if marker == nil {
		read, err := layout.ReadMarker(p.dest)
		if err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		marker = read
	}
	if marker != nil && marker.HasDAM() {
		return &DAMError{DAM: marker.DAM, Marker: marker.Path()}
	}
	return nil
}

// identify reads every group master's metadata in one batch and computes
// its identity.
//
// Only the capture-time tags are asked for. A whole-tag dump is multiple
// kilobytes per file for anything carrying an edit history, and an
// archive-sized run would pay that in pipe throughput for data nothing
// reads. The names are passed bare so that every group they appear in
// comes back: it is the group that decides which of two identically
// named times to believe, and ranking cannot rank what it was not given.
func (p *planner) identify() {
	p.identities = make(map[string]resolved, len(p.opts.Scan.Groups))

	keys := make([]string, 0, len(p.opts.Scan.Groups))
	paths := make([]string, 0, len(p.opts.Scan.Groups))
	for _, group := range p.opts.Scan.Groups {
		master, ok := masterOf(group.Members)
		if !ok {
			// A group with no master is either a stray sidecar whose
			// master this run never saw, or a file in a format stampla
			// owns nothing about — which only the membership check ever
			// collects, and which has its own thing to say.
			reason := "no master in this group: nothing carries an identity to share"
			if unowned(master.Path) {
				reason = unownedDetail
			}
			p.identities[group.ID()] = resolved{master: master, err: errors.New(reason)}
			continue
		}
		p.identities[group.ID()] = resolved{master: master}
		keys = append(keys, group.ID())
		paths = append(paths, absPath(master.Path))
	}

	p.opts.Progress.emit(PhaseRead, 0, len(paths), "")
	var metadata []exif.Metadata
	if len(paths) > 0 {
		metadata = p.opts.Pool.Read(paths, chainTags())
	}
	p.opts.Progress.emit(PhaseRead, len(paths), len(paths), "")

	// A format ExifTool isolates no payload in gets its whole file
	// hashed instead. That is deliberately a second pass: it is the
	// exception, and hashing every file "just in case" would double the
	// bytes read for an archive of RAWs that never needs it.
	var fallback []string
	for i, md := range metadata {
		if md.Err == nil && md.ImageDataHash == "" {
			fallback = append(fallback, paths[i])
		}
	}
	p.seeded = hashFiles(fallback, p.opts.Workers, p.opts.Progress)

	for i, md := range metadata {
		entry := p.identities[keys[i]]
		fileHash := ""
		if result, ok := p.seeded[paths[i]]; ok {
			if result.err != nil {
				entry.err = fmt.Errorf("%s: %w", paths[i], result.err)
				p.identities[keys[i]] = entry
				continue
			}
			fileHash = result.digest
		}
		entry.payload = md.ImageDataHash
		if entry.payload == "" {
			entry.payload = fileHash
		}
		entry.id, entry.prov, entry.err = identity.Compute(md, fileHash)
		p.identities[keys[i]] = entry
	}
}

// targetsOf works out where every member of a group would land.
//
// Both the pass that decides which files have to be read and the pass
// that classifies them go through here, and the answer is remembered, so
// the two can never disagree about where a file was going — a
// disagreement that would mean comparing the contents of the wrong pair
// of files and calling the result convergence.
func (p *planner) targetsOf(group scanner.Group) groupTargets {
	if cached, done := p.targets[group.ID()]; done {
		return cached
	}
	entry := p.identities[group.ID()]
	out := groupTargets{entry: entry, base: filepath.Base(group.Key)}
	if entry.err == nil {
		out.masterDir = filepath.Dir(absPath(entry.master.Path))
		if p.opts.Mode == VerifyMembership {
			// The membership question is about the place this archive
			// files things, which is the layout and nothing else.
			out.dir = filepath.Join(p.dest,
				filepath.FromSlash(p.opts.Resolution.Pattern.Dir(entry.id.Time)))
			out.entering = true
		} else {
			out.dir, out.entering = p.groupDir(out.masterDir, entry.id.Time)
		}
		for _, member := range group.Members {
			source := absPath(member.Path)
			dir, ok := memberTargetDir(out.masterDir, source, out.dir)
			if !ok {
				out.stray, out.strayed, out.members = member, true, nil
				break
			}
			out.members = append(out.members, memberTarget{
				item:   member,
				source: source,
				target: filepath.Join(dir,
					targetBase(filepath.Base(member.Path), out.base, entry.id)),
			})
		}
	}
	p.targets[group.ID()] = out
	return out
}

// probeSubstitution reads both sides of every content comparison the
// plan is going to have to make.
//
// The comparison is over the whole file, and that is the point. A
// payload digest names a photograph; it does not say that one file can
// stand in for another, because keywords, GPS, copyright and every other
// thing a person adds live in the metadata the payload digest
// deliberately excludes. Treating payload equality as substitutability
// is how a tool abandons the only copy of somebody's captions and
// reports success: "already present", exit zero, format the card.
//
// So: byte-identical is converged, and anything else is a conflict for a
// person to settle. The payload digest still names the file, and is read
// here only for occupied master targets, to say which kind of conflict
// an occupied name is — a different photograph, or the same one carrying
// metadata this run would have thrown away.
func (p *planner) probeSubstitution() {
	claims := make(map[string][]string)
	var needed, occupied []string

	for _, group := range p.opts.Scan.Groups {
		targets := p.targetsOf(group)
		if targets.entry.err != nil || targets.strayed {
			continue
		}
		for i, member := range targets.members {
			if member.source == member.target {
				continue
			}
			name := filepath.Base(member.target)
			if actual, present := p.entryAt(filepath.Dir(member.target), name); present &&
				actual == name {
				needed = append(needed, member.source, member.target)
				if i == 0 {
					occupied = append(occupied, member.target)
				}
			}
			key := foldKey(member.target)
			claims[key] = append(claims[key], member.source)
		}
	}
	// Two sources deriving one name have to be compared with each other
	// as well as with whatever is already there.
	for _, sources := range claims {
		if len(sources) > 1 {
			needed = append(needed, sources...)
		}
	}

	slices.Sort(needed)
	p.wholes = make(map[string]hashResult)
	var unread []string
	for _, path := range slices.Compact(needed) {
		// A master with no payload ExifTool could isolate was already
		// hashed whole for its identity; that digest is the same digest.
		if seeded, ok := p.seeded[path]; ok {
			p.wholes[path] = seeded
			continue
		}
		unread = append(unread, path)
	}
	for path, result := range hashFiles(unread, p.opts.Workers, p.opts.Progress) {
		p.wholes[path] = result
	}

	slices.Sort(occupied)
	p.occupants = p.contentDigests(slices.Compact(occupied))
}

// contentDigests reads, for each path, the digest an identity would be
// cut from: ExifTool's payload hash where the format has one, and the
// whole-file digest where it has none. Both sides of every content
// comparison go through here, so the two are always cut the same way.
func (p *planner) contentDigests(paths []string) map[string]hashResult {
	if len(paths) == 0 {
		return nil
	}
	digests := make(map[string]hashResult, len(paths))
	var fallback []string
	for _, md := range p.opts.Pool.Read(paths, chainTags()) {
		switch {
		case md.Err != nil:
			digests[md.Path] = hashResult{err: md.Err}
		case md.ImageDataHash != "":
			digests[md.Path] = hashResult{digest: md.ImageDataHash}
		default:
			fallback = append(fallback, md.Path)
		}
	}
	for path, result := range hashFiles(fallback, p.opts.Workers, p.opts.Progress) {
		digests[path] = result
	}
	return digests
}

// chainTags is the bare tag names both capture-time chains rank, sorted
// and deduplicated. Bare rather than group-qualified: a qualified name
// narrows the read to one group, and ranking needs every group the tag
// appears in to choose between them.
func chainTags() []string {
	seen := make(map[string]bool)
	var tags []string
	for _, entry := range identity.DefaultChain {
		name, _ := strings.CutSuffix(entry, "@utc")
		if _, tag, qualified := strings.Cut(name, ":"); qualified {
			name = tag
		}
		if !seen[name] {
			seen[name] = true
			tags = append(tags, name)
		}
	}
	slices.Sort(tags)
	return tags
}

// masterOf is the group's hash-carrying member: the file whose content
// names the whole group. The scan already ordered members master first,
// so this only has to confirm that the first one can carry an identity —
// a group of sidecars, or of derivatives whose master is not in the run,
// has none, and nothing may be renamed from a name that was never an
// identity of its own.
func masterOf(members []scanner.Item) (scanner.Item, bool) {
	if len(members) == 0 {
		return scanner.Item{}, false
	}
	first := members[0]
	base := filepath.Base(first.Path)
	if id, ok := identity.ParseName(base); ok {
		return first, id.IsMaster()
	}
	return first, identity.IsMedia(base) && !identity.IsSidecar(base)
}

// planGroup decides one group.
func (p *planner) planGroup(group scanner.Group) {
	for _, member := range group.Members {
		if under(p.dest, absPath(member.Path)) {
			p.plan.UnderRoot++
		}
	}

	entry := p.identities[group.ID()]
	gp := GroupPlan{
		// The group's ID, never its bare key: a still and a clip sharing
		// a base name are two groups, and every map that reaches back to
		// a group from a result keys on this.
		Key:        group.ID(),
		Kind:       group.Kind,
		Master:     entry.master.Path,
		Identity:   entry.id,
		Provenance: entry.prov,
	}
	if entry.err != nil {
		p.refuse(&gp, entry.master, finding.Unresolvable, entry.err.Error())
		return
	}

	targets := p.targetsOf(group)
	if p.opts.Mode.mutating() {
		if root, nested := p.nestedRoot(targets.masterDir); nested {
			p.refuse(&gp, entry.master, finding.Conflict, fmt.Sprintf(
				"belongs to the archive at %s; another archive inside this one is"+
					" not this run's business", root))
			return
		}
	}
	if targets.strayed {
		p.refuse(&gp, targets.stray, finding.Conflict, fmt.Sprintf(
			"sits outside its group's home directory %s; two homes for one name"+
				" is a question only a person can answer", targets.masterDir))
		return
	}

	if p.opts.Mode == VerifyMembership {
		p.planMembership(&gp, targets)
		return
	}
	if class, detail := alarmOf(entry); class != "" {
		p.refuse(&gp, entry.master, class, detail)
		return
	}

	actions := make([]Action, 0, len(targets.members))
	for i, member := range targets.members {
		// The master is the group's only identified member: it is what
		// tells a conflict over an occupied name from a conflict over
		// the metadata attached to one.
		actions = append(actions, p.classify(targets, member, i == 0))
	}
	if !p.settleClaims(&gp, targets) {
		return
	}
	gp.Actions = actions
	gp.Class = groupClass(actions)
	p.commit(gp)
}

// groupClass is the group's own disposition: the worst thing any of its
// members has to say. A group holding one stale file and nine converged
// ones has work to do, and a report that led with "converged" would bury
// it.
func groupClass(actions []Action) finding.Class {
	class := finding.Converged
	for _, action := range actions {
		switch {
		case action.Class.Alarm():
			return action.Class
		case action.Class == finding.Conflict:
			class = finding.Conflict
		case action.Class.Pending() && class == finding.Converged:
			class = action.Class
		}
	}
	return class
}

// planMembership answers "is this group accounted for in that archive".
//
// It derives where every member would live under the destination's
// layout, looks, and — where something is there — compares the two files
// whole. Presence at the right name is not the answer on its own: this
// exit code is what a person reads before formatting the card, so
// "accounted for" has to mean the archive holds this file, not a file
// that would be given the same name. A copy differing only in metadata
// is a copy missing whatever that metadata was.
func (p *planner) planMembership(gp *GroupPlan, targets groupTargets) {
	actions := make([]Action, 0, len(targets.members))
	for _, member := range targets.members {
		action := Action{
			Old:     member.item.Path,
			New:     member.target,
			Implied: member.item.Implied,
		}
		switch {
		case unowned(member.item.Path):
			action.Class = finding.Unresolvable
			action.Detail = unownedDetail
		case !p.exists(member.target):
			action.Class = finding.Missing
			action.Detail = "not present at " + member.target
		default:
			state, detail := p.substitutable(member.source, member.target, "")
			if state == occTaken {
				action.Class = finding.Converged
				action.Detail = "accounted for at " + member.target
			} else {
				action.Class = finding.Conflict
				action.Detail = detail
			}
		}
		actions = append(actions, action)
	}
	gp.Actions = actions
	gp.Class = groupClass(actions)
	p.commit(*gp)
}

// unownedDetail is what a file stampla owns no identity for is told.
const unownedDetail = "format not owned; cannot be accounted for"

// unowned reports whether stampla owns neither an identity for this file
// nor a reason to rename it. Such a file only ever reaches a plan
// through scanner.Options.KeepUnowned, which the membership check sets:
// its answer is about a whole card, and a file the report never
// mentioned is a file that answer did not cover.
func unowned(path string) bool {
	return !identity.IsMedia(path) && !identity.IsSidecar(path)
}

// groupDir is where a group belongs, and whether it is entering the
// archive from outside.
//
// A file entering the root is placed by the resolved layout, whatever
// that layout's provenance: it has no directory here yet, so a default
// is the only thing that could place it. A file already under the root
// keeps its directory unless the layout was declared for this
// destination — a fallback may place new files, but it may never
// reorganize a tree somebody else arranged.
func (p *planner) groupDir(masterDir string, when time.Time) (dir string, entering bool) {
	pattern := filepath.Join(p.dest, filepath.FromSlash(p.opts.Resolution.Pattern.Dir(when)))
	if !under(p.dest, masterDir) {
		return pattern, true
	}
	if p.opts.Resolution.Declared {
		return pattern, false
	}
	return masterDir, false
}

// memberTargetDir keeps a member's offset from its group's home. The one
// offset that occurs is a vendor sidecar subdirectory (NKSC_PARAM), one
// level below the master; anything further out means the group has
// members in two places, which relocating would have to guess about.
func memberTargetDir(masterDir, memberPath, targetDir string) (string, bool) {
	rel, err := filepath.Rel(masterDir, filepath.Dir(memberPath))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return targetDir, true
	}
	return filepath.Join(targetDir, rel), true
}

// alarmOf compares what the master's name claims with what its content
// says, and names the damage when the two disagree on a format that is
// never edited in place.
//
// A write-once file is never renamed on a mismatch, under any mode: its
// old name is the only surviving record of what its identity used to be,
// and renaming would turn evidence of damage into a plausible file. The
// hash and the time are separate alarms because ImageDataHash covers the
// payload alone — a damaged metadata region must not pass as somebody
// having edited a date.
func alarmOf(entry resolved) (finding.Class, string) {
	parsed, ok := identity.ParseName(filepath.Base(entry.master.Path))
	if !ok {
		return "", ""
	}
	writeOnce := identity.IsWriteOnce(parsed.Ext)
	switch {
	case parsed.Hash != entry.id.Hash:
		if !writeOnce {
			return "", ""
		}
		return finding.Corrupt, fmt.Sprintf(
			"the name says the %s hash is %s, the file's is %s — %s is never renamed"+
				" on a content mismatch, because the old name is the only record of"+
				" what this file used to be",
			entry.prov.Hash, parsed.Hash, entry.id.Hash, parsed.Ext)
	case !parsed.Time.Equal(entry.id.Time):
		if !writeOnce {
			return "", ""
		}
		return finding.TimeDrift, fmt.Sprintf(
			"%s says %s, the name says %s, and the %s hash still matches —"+
				" a metadata region that changed under a write-once format is"+
				" damage until a person says otherwise",
			entry.prov.TimeTag, entry.id.Time.Format(evidenceTime),
			parsed.Time.Format(evidenceTime), entry.prov.Hash)
	}
	return "", ""
}

// evidenceTime is how a capture time is quoted back in a finding: the
// canonical name's own form, so a report and a filename read alike.
const evidenceTime = "20060102_150405"

// classify decides one member.
func (p *planner) classify(targets groupTargets, member memberTarget, isMaster bool) Action {
	entry := targets.entry
	oldAbs, newAbs := member.source, member.target
	targetDir, newName := filepath.Dir(newAbs), filepath.Base(newAbs)
	action := Action{Old: member.item.Path, New: newAbs, Implied: member.item.Implied}

	if unowned(member.item.Path) {
		// Only the membership check ever collects one of these, and it
		// classifies them itself; reaching here means a caller asked a
		// mutation verb to keep them. Renaming a file stampla owns no
		// identity for is not something a mode flag may turn on.
		action.Class = finding.Unresolvable
		action.Detail = unownedDetail
		return action
	}

	payload := ""
	if isMaster {
		payload = entry.payload
	}
	switch state, detail := p.occupancy(oldAbs, newAbs, targetDir, newName, payload); state {
	case occSelf:
		action.Class = finding.Converged
		action.New = ""
		action.Detail = "name, hash and location all match"
		return action
	case occOther:
		action.Class = finding.Conflict
		action.Detail = detail
		return action
	case occClaim:
		// One file under two names: an interrupted run linked its target
		// and stopped before dropping the source. The target is provably
		// the same inode, so finishing the unlink deletes nothing.
		action.Class = finding.Converged
		action.Detail = "already at its identity through an interrupted rename; " + detail
		if p.opts.Mode == Move {
			action.Verb = VerbUnlink
		} else {
			action.New = ""
		}
		return action
	case occTaken:
		// Byte for byte the same file, so the archive's copy really does
		// stand in for this one. The source is still left exactly where
		// it is: a source is only ever deleted after its own copy has
		// been verified, and finding an equal file already there is not
		// this run having made one.
		action.Class = finding.Converged
		action.Detail = "already present at " + newAbs
		if p.opts.Mode.mutating() {
			action.Detail += "; the source is left where it is"
		}
		return action
	}

	action.Class, action.Detail = nameClass(
		member.item.Path, newName, targetDir, targets.base, targets.entering, entry)
	action.Verb = p.verbFor(action.Class, targets.entering)
	switch {
	case action.Verb == VerbNone && p.opts.Mode == Copy && action.Class != finding.Converged:
		action.Detail += "; cp never renames a file that is already under the" +
			" destination — mv is the verb that does"
	case action.Verb != VerbNone:
		// Resolved before anything is written, and again at apply time:
		// a directory inside the archive can be a symlink out of it, and
		// every no-clobber guarantee only ever covered the target name.
		if err := p.containment(targetDir); err != nil {
			action.Class = finding.Conflict
			action.Verb = VerbNone
			action.Detail = err.Error()
		}
	}
	return action
}

// nameClass is the disposition of a file whose target is free: what its
// current name says about it, and what has to happen for it to wear its
// identity.
//
// The line between stale and incoming is whether the name carries an
// identity at all. A file already named from a capture time and a
// content hash is claiming something the file no longer supports, which
// is what stale means; a file that was never named is not disagreeing
// with anything, it is simply being given a name. The question is asked
// of the group's key rather than of the member: a sidecar's name carries
// its master's identity in the master's grammar, not in one of its own.
func nameClass(path, newName, targetDir, groupBase string,
	entering bool, entry resolved,
) (finding.Class, string) {
	named := fmt.Sprintf("named %s from %s (%s hash)",
		newName, entry.prov.TimeTag, entry.prov.Hash)
	switch {
	case entering:
		return finding.Incoming, named
	case filepath.Base(path) == newName:
		return finding.Misplaced, fmt.Sprintf(
			"sits in %s but its name belongs in %s", filepath.Dir(absPath(path)), targetDir)
	case isIdentityPrefix(groupBase):
		return finding.Stale, fmt.Sprintf("the name no longer matches the file; %s", named)
	default:
		return finding.Incoming, named
	}
}

// isIdentityPrefix reports whether a group key is an identity rather
// than the base name of a file that has never had one.
//
// It asks identity's own grammar instead of restating it: a prefix is
// exactly what is left of a canonical name when its extension is taken
// off, so putting one back is the question. Restating the grammar here
// would be a second definition of a canonical name, and the two would
// eventually disagree about some file.
func isIdentityPrefix(base string) bool {
	_, ok := identity.ParseName(base + ".jpg")
	return ok
}

// verbFor is what Apply does about a class in this mode.
//
// cp deliberately does nothing to a file that already sits under the
// destination: copying it to a second name would leave the archive
// holding the same photo twice, which is the opposite of converging. The
// file is still classified and reported, and mv is the verb that acts on
// it.
func (p *planner) verbFor(class finding.Class, entering bool) Verb {
	switch class {
	case finding.Incoming, finding.Stale, finding.Misplaced:
	default:
		return VerbNone
	}
	switch p.opts.Mode {
	case Copy:
		if entering {
			return VerbCopy
		}
		return VerbNone
	case Move:
		return VerbMove
	default:
		return VerbNone
	}
}

// occupancy is what already sits at a target path.
type occupancy int

const (
	// occFree: nothing is there.
	occFree occupancy = iota
	// occSelf: the target is the file itself, already in place.
	occSelf
	// occTaken: another file already wears this identity name.
	occTaken
	// occClaim: the source itself, hard-linked at the target by an
	// interrupted rename.
	occClaim
	// occOther: something that is not this identity and must not be
	// overwritten.
	occOther
)

// occupancy inspects a target path without touching it.
//
// What the check must not miss is a target that only looks free or only
// looks taken: on a case-insensitive filesystem a differently spelled
// entry occupies the slot, and a rename into it would overwrite a file
// whose name never appeared in any plan. Whether an occupant can stand
// in for the source is substitutable's question, not this one's.
func (p *planner) occupancy(oldAbs, newAbs, dir, name, payload string) (occupancy, string) {
	if oldAbs == newAbs {
		return occSelf, ""
	}
	actual, present := p.entryAt(dir, name)
	if !present {
		return occFree, ""
	}
	if filepath.Dir(oldAbs) == dir && actual == filepath.Base(oldAbs) {
		// The occupant is the source itself under another spelling: on a
		// case-insensitive filesystem .JPG and .jpg are one directory
		// entry, so this is a rename of the entry rather than a
		// collision with a second file.
		return occFree, ""
	}
	if actual != name {
		return occOther, fmt.Sprintf(
			"%s is occupied by %s, which this filesystem treats as the same name",
			newAbs, actual)
	}
	oldInfo, err := os.Lstat(oldAbs)
	if err != nil {
		return occOther, fmt.Sprintf("%s: %v", oldAbs, err)
	}
	newInfo, err := os.Lstat(newAbs)
	if err != nil {
		return occOther, fmt.Sprintf("%s: %v", newAbs, err)
	}
	if !newInfo.Mode().IsRegular() {
		return occOther, newAbs + " is not a regular file"
	}
	if os.SameFile(oldInfo, newInfo) {
		return occClaim, "the leftover source link at " + oldAbs + " is dropped"
	}
	return p.substitutable(oldAbs, newAbs, payload)
}

// substitutable asks whether the file already at the target can stand in
// for the source, and it asks it of the whole file.
//
// Payload equality is not file equality. Keywords, captions, GPS,
// copyright, ratings, the whole record of what somebody did with a
// photograph — all of it lives in the metadata that the payload digest
// deliberately excludes, which is exactly what makes that digest a
// stable name. Reusing it as a substitutability test says "already
// present" about a file that is missing everything a person added, and
// then a card is formatted over the only copy of it.
//
// So the answer is byte-identical or nothing. Anything else is a
// conflict, and where the payloads do match it is named as what it is,
// because "same photograph, different metadata" is a thing a person can
// act on and "different content" is not.
func (p *planner) substitutable(source, target, payload string) (occupancy, string) {
	here, read := p.wholes[source]
	there, probed := p.wholes[target]
	switch {
	case !read || !probed:
		return occOther, fmt.Sprintf("%s is occupied and the two were not compared", target)
	case here.err != nil:
		return occOther, fmt.Sprintf("%s could not be read: %v", source, here.err)
	case there.err != nil:
		return occOther, fmt.Sprintf("%s is occupied and could not be read: %v", target, there.err)
	case here.digest == there.digest:
		return occTaken, ""
	}
	if payload != "" {
		if held, ok := p.occupants[target]; ok && held.err == nil && held.digest == payload {
			return occOther, fmt.Sprintf(
				"%s holds the same image data but different metadata — the two files"+
					" are not interchangeable and neither is touched; resolve by hand",
				target)
		}
	}
	return occOther, fmt.Sprintf(
		"%s already exists and holds different content; neither file is touched", target)
}

// short cuts a digest to the slice a name carries, which is the form a
// reader can compare against a filename at a glance.
func short(digest string) string {
	if len(digest) <= identity.HashLength {
		if digest == "" {
			return "no readable payload"
		}
		return digest
	}
	return digest[:identity.HashLength]
}

// settleClaims resolves two groups of one run deriving the same target
// paths.
//
// The same frame written twice by a camera converges once: reporting the
// second as work would be reporting work that must not happen. But
// "twice" has to mean byte-identical, member for member — two files with
// one payload and different metadata are two different files, and
// silently keeping either one of them is silently discarding the other's
// captions. Every member is compared, not only the master: a group whose
// sidecar differs is not the same group.
func (p *planner) settleClaims(gp *GroupPlan, targets groupTargets) bool {
	entry := targets.entry
	mine := p.claimDigests(targets)
	for _, member := range targets.members {
		if member.source == member.target {
			continue
		}
		held, taken := p.claimed[foldKey(member.target)]
		if !taken || held.key == gp.Key {
			continue
		}
		switch {
		case sameClaim(mine, held.digests):
			p.refuse(gp, entry.master, finding.Converged, fmt.Sprintf(
				"byte for byte the same files as %s, which already converges to %s;"+
					" converged once", held.master, member.target))
		case held.payload != "" && held.payload == entry.payload:
			p.refuse(gp, entry.master, finding.Conflict, fmt.Sprintf(
				"derives %s, the same name as %s, and holds the same image data with"+
					" different metadata — the two are not interchangeable and neither"+
					" is taken; resolve by hand", member.target, held.master))
		default:
			p.refuse(gp, entry.master, finding.Conflict, fmt.Sprintf(
				"derives %s, the same name as %s, but the content differs;"+
					" neither is taken", member.target, held.master))
		}
		return false
	}
	held := claim{
		key: gp.Key, master: entry.master.Path, payload: entry.payload, digests: mine,
	}
	for target := range mine {
		p.claimed[target] = held
	}
	return true
}

// claimDigests is what a group would put at each name it takes.
func (p *planner) claimDigests(targets groupTargets) map[string]string {
	digests := make(map[string]string, len(targets.members))
	for _, member := range targets.members {
		if member.source != member.target {
			digests[foldKey(member.target)] = p.wholes[member.source].digest
		}
	}
	return digests
}

// sameClaim reports whether two groups would put the very same bytes at
// the very same names. A digest that could not be computed never counts
// as a match: an unread file is not a proven duplicate.
func sameClaim(mine, held map[string]string) bool {
	if len(mine) == 0 || len(mine) != len(held) {
		return false
	}
	for target, digest := range mine {
		if other, ok := held[target]; !ok || digest == "" || digest != other {
			return false
		}
	}
	return true
}

// foldKey is a target path as the filesystem would compare it, so two
// plans for one file are recognized as one claim.
func foldKey(path string) string {
	if caseInsensitiveFS {
		return strings.ToLower(path)
	}
	return path
}

// targetBase is the name a member takes when its group converges.
//
// Only the prefix ever changes. A canonical name is rebuilt from its own
// parts, so a derivative label and an appended master extension survive
// untouched. A name that is not canonical keeps everything after the
// base its group is keyed by — the label, the extensions, in that order
// — with the extensions lowercased, which is what turns DSC1234-Edit.TIF
// beside DSC1234.NEF into <prefix>-Edit.tif.
func targetBase(base, groupBase string, id identity.Identity) string {
	if parsed, ok := identity.ParseName(base); ok {
		return identity.Identity{
			Time: id.Time, Hash: id.Hash,
			Suffix: parsed.Suffix, RawExt: parsed.RawExt, Ext: parsed.Ext,
		}.Name()
	}
	remainder := ""
	switch {
	case strings.HasPrefix(base, groupBase):
		remainder = base[len(groupBase):]
	default:
		// A member that does not carry its group's base at all: keep its
		// extensions and drop the stem, which is the part the prefix
		// replaces.
		if _, extensions, found := strings.Cut(base, "."); found {
			remainder = "." + extensions
		}
	}
	label, extensions, found := strings.Cut(remainder, ".")
	name := id.Prefix() + label
	if found {
		name += "." + strings.ToLower(extensions)
	}
	return name
}

// refuse records a group nothing may be done to, with the member
// responsible and the reason.
//
// A refused group reports once. Its other members are deliberately not
// classified: the group is the unit of work, so until the reason is
// resolved nothing about where they belong is decided, and listing them
// as pending work would invite exactly the per-file action the refusal
// exists to prevent.
func (p *planner) refuse(gp *GroupPlan, member scanner.Item, class finding.Class, detail string) {
	gp.Class = class
	gp.Detail = detail
	gp.Refused = true
	gp.Actions = []Action{{
		Class:   class,
		Old:     member.Path,
		Detail:  detail,
		Implied: member.Implied,
	}}
	p.commit(*gp)
}

// commit files a decided group and its findings.
func (p *planner) commit(gp GroupPlan) {
	p.plan.Groups = append(p.plan.Groups, gp)
	for _, action := range gp.Actions {
		if action.Verb != VerbNone && under(p.dest, absPath(action.Old)) {
			p.plan.Touched++
		}
		p.plan.Findings = append(p.plan.Findings, finding.Finding{
			Class:  action.Class,
			Path:   action.Old,
			Old:    action.Old,
			New:    action.New,
			Detail: action.Detail,
		})
	}
}

// collect totals the findings by class.
func (p *planner) collect() {
	for _, f := range p.plan.Findings {
		p.plan.Counts[f.Class]++
	}
}

// entryAt reports the name a directory actually holds for a wanted name,
// and whether it holds one at all.
//
// Where the platform's filesystems fold case, an entry that differs only
// by spelling is reported as it is actually spelled: it is the same
// name there, and a caller that believed the target free would overwrite
// a file that appeared in no plan. Where they do not fold, two spellings
// are two files and only an exact match counts — treating them as one
// would refuse writes to a name that is free, and, worse, would let a
// membership check call a card accounted for by a file that is not it.
func (p *planner) entryAt(dir, name string) (string, bool) {
	names, cached := p.entries[dir]
	if !cached {
		entries, err := os.ReadDir(dir)
		if err == nil {
			names = make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
		}
		p.entries[dir] = names
	}
	if slices.Contains(names, name) {
		return name, true
	}
	if caseInsensitiveFS {
		for _, candidate := range names {
			if strings.EqualFold(candidate, name) {
				return candidate, true
			}
		}
	}
	return "", false
}

// containment resolves a target directory once per directory. Every
// file landing in one asks the same question, and the answer involves
// walking the path through the filesystem's own symlink table.
func (p *planner) containment(dir string) error {
	if err, asked := p.contained[dir]; asked {
		return err
	}
	err := contained(p.dest, dir)
	p.contained[dir] = err
	return err
}

// exists reports whether a path is present, through the same cached
// listings the occupancy check uses.
func (p *planner) exists(path string) bool {
	_, ok := p.entryAt(filepath.Dir(path), filepath.Base(path))
	return ok
}

// nestedRoot reports the archive root a directory under the destination
// belongs to, when that is not the destination itself. Another archive
// inside this one is not this run's business: its own marker declares
// its own layout, and converging its files under this root's would file
// them where its own next run would not look.
func (p *planner) nestedRoot(dir string) (string, bool) {
	if !under(p.dest, dir) || dir == p.dest {
		return "", false
	}
	root, cached := p.roots[dir]
	if !cached {
		marker, err := layout.NearestRoot(dir)
		if err == nil && marker != nil {
			root = absPath(marker.Dir)
		}
		p.roots[dir] = root
	}
	return root, root != "" && root != p.dest && under(p.dest, root)
}

// damArtifacts finds the catalogs and sessions of a digital asset
// manager beside or directly inside the destination.
//
// Their presence is reported and nothing more: a catalog tracks files by
// path, so moving files it knows about orphans its entries, and the
// caller turns that into a confirmation. The search is deliberately
// shallow — the destination's own entries and its parent's — because it
// must be cheap, deterministic, and run before anything else does.
func damArtifacts(dest string) []string {
	dirs := []string{dest}
	if parent := filepath.Dir(dest); parent != dest {
		dirs = append(dirs, parent)
	}
	var found []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if isDAMArtifact(entry.Name()) {
				found = append(found, filepath.Join(dir, entry.Name()))
			}
		}
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// isDAMArtifact recognizes a Lightroom Classic catalog (and the
// directory of sidecar data beside it) or a Capture One catalog or
// session.
func isDAMArtifact(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".lrcat", ".lrcat-data", ".cocatalog"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.Contains(lower, ".cosession")
}
