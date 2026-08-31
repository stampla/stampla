package engine

import (
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
	p.probeOccupants()
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

	// identities holds one entry per group, keyed by the group key.
	identities map[string]resolved
	// entries caches directory listings, so a target directory is read
	// once however many files land in it.
	entries map[string][]string
	// roots caches the nearest archive root of a directory.
	roots map[string]string
	// claimed records which target path each group took, so that two
	// sources deriving one name are settled rather than raced.
	claimed map[string]claim
	// occupants holds the content digest of every occupied master
	// target, so that a file already wearing an identity name can be
	// asked whether it really holds that content.
	occupants map[string]hashResult
	// contained caches the containment verdict of each target
	// directory.
	contained map[string]error
}

// resolved is a group master's computed identity, or why it has none.
type resolved struct {
	master scanner.Item
	id     identity.Identity
	prov   identity.Provenance
	// digest is the full content hash the identity was cut from, kept
	// for comparing two sources that derive the same name.
	digest string
	err    error
}

// claim records the group that took a target path first.
type claim struct {
	key    string
	master string
	digest string
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
			p.identities[group.Key] = resolved{
				master: master,
				err:    fmt.Errorf("no master in this group: nothing carries an identity to share"),
			}
			continue
		}
		p.identities[group.Key] = resolved{master: master}
		keys = append(keys, group.Key)
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
	digests := hashFiles(fallback, p.opts.Workers, p.opts.Progress)

	for i, md := range metadata {
		entry := p.identities[keys[i]]
		fileHash := ""
		if result, ok := digests[paths[i]]; ok {
			if result.err != nil {
				entry.err = fmt.Errorf("%s: %w", paths[i], result.err)
				p.identities[keys[i]] = entry
				continue
			}
			fileHash = result.digest
		}
		entry.digest = md.ImageDataHash
		if entry.digest == "" {
			entry.digest = fileHash
		}
		entry.id, entry.prov, entry.err = identity.Compute(md, fileHash)
		p.identities[keys[i]] = entry
	}
}

// probeOccupants reads the content of every master target that is
// already taken.
//
// A file sitting under an identity name is claiming that identity, and
// the claim is worth exactly as much as its content. Re-importing a
// memory card has to tell "this photo is already here" from "a different
// photo is already under this name": the first converges silently, the
// second is a conflict no plan may guess its way past, and only the
// payload digest separates them. It is compared rather than the whole
// file so that an archive copy somebody has since added keywords to
// still counts as the same photograph — which is the entire point of
// naming from the payload.
//
// Only master targets are read. A sidecar carries no identity of its
// own, so there is nothing about it to verify, and reading every one
// would double an import's metadata traffic for an answer nobody uses.
// The membership check reads nothing here at all: its question is
// presence at the place this archive files things, and it must never
// pay for a deep verify it was not asked for.
func (p *planner) probeOccupants() {
	if p.opts.Mode == VerifyMembership {
		return
	}
	var targets []string
	for _, group := range p.opts.Scan.Groups {
		target, ok := p.masterTarget(group)
		if !ok {
			continue
		}
		if actual, present := p.entryAt(filepath.Dir(target), filepath.Base(target)); present &&
			actual == filepath.Base(target) {
			targets = append(targets, target)
		}
	}
	slices.Sort(targets)
	p.occupants = p.contentDigests(slices.Compact(targets))
}

// masterTarget is where a group's master would land, when the group has
// one and is not already there.
func (p *planner) masterTarget(group scanner.Group) (string, bool) {
	entry := p.identities[group.Key]
	if entry.err != nil {
		return "", false
	}
	masterAbs := absPath(entry.master.Path)
	targetDir, _ := p.groupDir(filepath.Dir(masterAbs), entry.id.Time)
	target := filepath.Join(targetDir,
		targetBase(filepath.Base(entry.master.Path), filepath.Base(group.Key), entry.id))
	return target, target != masterAbs
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

	entry := p.identities[group.Key]
	gp := GroupPlan{
		Key:        group.Key,
		Master:     entry.master.Path,
		Identity:   entry.id,
		Provenance: entry.prov,
	}
	if entry.err != nil {
		p.refuse(&gp, entry.master, finding.Unresolvable, entry.err.Error())
		return
	}

	masterDir := filepath.Dir(absPath(entry.master.Path))
	if p.opts.Mode.mutating() {
		if root, nested := p.nestedRoot(masterDir); nested {
			p.refuse(&gp, entry.master, finding.Conflict, fmt.Sprintf(
				"belongs to the archive at %s; another archive inside this one is"+
					" not this run's business", root))
			return
		}
	}

	if p.opts.Mode == VerifyMembership {
		p.planMembership(&gp, group, entry)
		return
	}
	if class, detail := alarmOf(entry); class != "" {
		p.refuse(&gp, entry.master, class, detail)
		return
	}

	targetDir, entering := p.groupDir(masterDir, entry.id.Time)
	base := filepath.Base(group.Key)
	actions := make([]Action, 0, len(group.Members))
	for i, member := range group.Members {
		memberDir, ok := memberTargetDir(masterDir, absPath(member.Path), targetDir)
		if !ok {
			p.refuse(&gp, member, finding.Conflict, fmt.Sprintf(
				"sits outside its group's home directory %s; two homes for one name"+
					" is a question only a person can answer", masterDir))
			return
		}
		// The master is the group's only content-verified member: it is
		// what an occupied target has to be compared against.
		actions = append(actions, p.classify(entry, member, base, memberDir, entering, i == 0))
	}
	if !p.settleClaims(&gp, entry, actions) {
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
// It derives where every member would live under the destination's
// layout and looks. Nothing at the destination is hashed: the question
// is presence at the place this archive files it, and a name that is an
// identity is what makes presence answerable at all.
func (p *planner) planMembership(gp *GroupPlan, group scanner.Group, entry resolved) {
	masterDir := filepath.Dir(absPath(entry.master.Path))
	targetDir := filepath.Join(p.dest, filepath.FromSlash(p.opts.Resolution.Pattern.Dir(entry.id.Time)))
	base := filepath.Base(group.Key)

	actions := make([]Action, 0, len(group.Members))
	for _, member := range group.Members {
		memberDir, ok := memberTargetDir(masterDir, absPath(member.Path), targetDir)
		if !ok {
			p.refuse(gp, member, finding.Conflict, fmt.Sprintf(
				"sits outside its group's home directory %s", masterDir))
			return
		}
		expected := filepath.Join(memberDir, targetBase(filepath.Base(member.Path), base, entry.id))
		action := Action{Old: member.Path, New: expected, Implied: member.Implied}
		if p.exists(expected) {
			action.Class = finding.Converged
			action.Detail = "accounted for at " + expected
		} else {
			action.Class = finding.Missing
			action.Detail = "not present at " + expected
		}
		actions = append(actions, action)
	}
	gp.Actions = actions
	gp.Class = groupClass(actions)
	p.commit(*gp)
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
func (p *planner) classify(entry resolved, member scanner.Item, base, targetDir string, entering, isMaster bool) Action {
	oldAbs := absPath(member.Path)
	newName := targetBase(filepath.Base(member.Path), base, entry.id)
	newAbs := filepath.Join(targetDir, newName)
	action := Action{Old: member.Path, New: newAbs, Implied: member.Implied}

	expect := ""
	if isMaster {
		expect = entry.digest
	}
	switch state, detail := p.occupancy(oldAbs, newAbs, targetDir, newName, expect); state {
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
		// The name is the identity, so a file already sitting under this
		// name holds this content. The source is left exactly where it
		// is: it is accounted for, but a source is only ever deleted
		// after its own copy has been verified, and a matching name is
		// not that verification.
		action.Class = finding.Converged
		action.Detail = "already present at " + newAbs
		if p.opts.Mode.mutating() {
			action.Detail += "; the source is left where it is"
		}
		return action
	}

	action.Class, action.Detail = nameClass(member.Path, newName, targetDir, base, entering, entry)
	action.Verb = p.verbFor(action.Class, entering)
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
// The name is the identity, so a file sitting under an identity name is
// that identity — that is what lets a re-imported memory card converge
// instead of duplicating, with no database and no second read of the
// archive. What the check must not miss is a target that only looks free
// or only looks taken: on a case-insensitive filesystem a differently
// spelled entry occupies the slot, and a rename into it would overwrite
// a file whose name never appeared in any plan.
func (p *planner) occupancy(oldAbs, newAbs, dir, name, expect string) (occupancy, string) {
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
	if expect == "" {
		// A member with no identity of its own: its name is its master's,
		// and the master's content is what was verified.
		return occTaken, ""
	}
	held, probed := p.occupants[newAbs]
	switch {
	case !probed:
		return occOther, newAbs + " is occupied and its content was not read"
	case held.err != nil:
		return occOther, fmt.Sprintf("%s is occupied and could not be read: %v", newAbs, held.err)
	case held.digest == expect:
		return occTaken, ""
	default:
		return occOther, fmt.Sprintf(
			"%s already exists and holds different content (%s there, %s here);"+
				" neither file is touched", newAbs, short(held.digest), short(expect))
	}
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

// settleClaims resolves two groups deriving the same target path.
//
// Two files with one identity are two files with the same capture second
// and the same payload digest, which on a memory card means the camera
// wrote the same frame twice: it converges once, and reporting the
// second as work would be reporting work that must not happen. Content
// that genuinely differs under one name is a conflict no plan may guess
// its way past.
func (p *planner) settleClaims(gp *GroupPlan, entry resolved, actions []Action) bool {
	for _, action := range actions {
		if action.New == "" {
			continue
		}
		held, taken := p.claimed[foldKey(action.New)]
		if !taken || held.key == gp.Key {
			continue
		}
		if held.digest != "" && held.digest == entry.digest {
			p.refuse(gp, entry.master, finding.Converged, fmt.Sprintf(
				"identical content to %s, which already converges to %s; converged once",
				held.master, action.New))
			return false
		}
		p.refuse(gp, entry.master, finding.Conflict, fmt.Sprintf(
			"derives %s, the same name as %s, but the content differs;"+
				" neither is renamed", action.New, held.master))
		return false
	}
	for _, action := range actions {
		if action.New != "" {
			p.claimed[foldKey(action.New)] = claim{
				key: gp.Key, master: entry.master.Path, digest: entry.digest,
			}
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
