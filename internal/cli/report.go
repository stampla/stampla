package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
	"github.com/stampla/stampla/internal/scanner"
)

// archive is everything one examined archive has to say: the plan that
// examined it, what applying that plan did, and the provenance every
// report states before any of it.
type archive struct {
	root    string
	nested  bool
	mode    engine.Mode
	res     layout.Resolution
	plan    *engine.Plan
	result  *engine.Result
	skipped scanner.Skipped
	// notes are the lines that are not findings: marker warnings, hints,
	// and the questions a preview would have asked. Each carries its own
	// label, because a warning about a marker and a suggestion about a
	// layout are not the same kind of thing to be told.
	notes []string
	// unwritten says why nothing was applied, and is empty when
	// something was — or when the verb never writes at all.
	unwritten string
}

// outcome is what the run as a whole did, which is what a machine
// reader's last line carries.
type outcome struct {
	exit    int
	applied int
	failed  int
	receipt string
	marker  string
}

// reporter is how a run speaks: one implementation for a person, one
// for a program, and the run itself cannot tell which it has.
//
// head announces an archive and its layout provenance before the work
// on it starts, body reports that work once it is done, progress is the
// engine's callback, and tail closes the run.
type reporter interface {
	head(mode engine.Mode, root string, nested bool, res layout.Resolution)
	body(a archive)
	progress(phase engine.Phase, done, total int, path string)
	tail(o outcome)
}

// classOrder is the order a report speaks in: damage first, then what
// needs a person, then the work a run can do by itself, then what is
// already right.
var classOrder = []finding.Class{
	finding.Corrupt,
	finding.TimeDrift,
	finding.Conflict,
	finding.Missing,
	finding.Unresolvable,
	finding.Stale,
	finding.Misplaced,
	finding.Incoming,
	finding.Converged,
}

// entry is one line of a report: where a file is, where it belongs, and
// the evidence behind saying so.
type entry struct {
	from   string
	to     string
	detail string
}

// section is one titled block of a report.
type section struct {
	title string
	// alarm marks damage, which is what a report puts in red.
	alarm bool
	// evidence asks for the detail line under every entry, rather than
	// only under the ones that are going nowhere.
	evidence bool
	entries  []entry
}

// sections is the body of a report, in the order it is spoken.
//
// A run that applied its plan leads with what it did, taken from what
// actually landed and in the order it landed — the same pairs the
// receipt recorded. What follows is what the run did not resolve,
// grouped by class: for a preview that is the whole plan, and for an
// applied run it is what a person still has to deal with.
//
// Converged files are counted and never listed. An archive that
// verifies clean has nothing to say about twenty thousand files one at a
// time, and a report nobody reads to the end is a report that hides its
// alarms.
func sections(a archive) []section {
	var found []section
	if landed := landedSection(a); landed != nil {
		found = append(found, *landed)
	}
	buckets := make(map[finding.Class][]entry)
	for _, f := range remaining(a, true) {
		if f.Class == finding.Converged {
			continue
		}
		from := f.Old
		if from == "" {
			from = f.Path
		}
		buckets[f.Class] = append(buckets[f.Class], entry{from: from, to: f.New, detail: f.Detail})
	}
	// After a run that acted, everything still listed is something it
	// did not do, and the reason is the interesting part: cp does not
	// rename a file that is already in the archive, a conflict was
	// refused, a capture time would not resolve.
	explain := a.result != nil
	for _, class := range classOrder {
		if entries := buckets[class]; len(entries) > 0 {
			found = append(found, classSection(class, entries, explain))
			delete(buckets, class)
		}
	}
	// A class this order has not heard of is still reported, at the end
	// and in a fixed order: an unreported finding is the one thing a
	// report may never produce.
	rest := make([]string, 0, len(buckets))
	for class := range buckets {
		rest = append(rest, string(class))
	}
	slices.Sort(rest)
	for _, class := range rest {
		found = append(found, classSection(finding.Class(class), buckets[finding.Class(class)], explain))
	}
	return found
}

func classSection(class finding.Class, entries []entry, explain bool) section {
	return section{
		title:    string(class),
		alarm:    class.Alarm(),
		evidence: class.Alarm() || explain,
		entries:  entries,
	}
}

// landedSection is what an applied run actually did, titled by the verb
// that did it.
func landedSection(a archive) *section {
	if a.result == nil || len(a.result.Landed) == 0 {
		return nil
	}
	title := "copied"
	if a.mode == engine.Move {
		title = "moved"
	}
	entries := make([]entry, 0, len(a.result.Landed))
	for _, action := range a.result.Landed {
		entries = append(entries, entry{from: action.Old, to: action.New})
	}
	return &section{title: title, entries: entries}
}

// remaining is the findings a run did not resolve.
//
// Work that landed is not pending any more, which is why an import that
// worked exits 0 while its own preview exits 1: the preview's findings
// are all still true. A report also drops the findings of a group Apply
// could not land — those are reported as failures instead, because a
// rename that did not happen must never be printed as one that did —
// while the exit code keeps them, since something is still wrong with
// those files whatever the failure list says.
func remaining(a archive, forReport bool) []finding.Finding {
	if a.result == nil {
		return a.plan.Findings
	}
	done := make(map[string]bool, len(a.result.Landed))
	for _, action := range a.result.Landed {
		done[action.Old] = true
	}
	if forReport {
		for path := range failedPaths(a) {
			done[path] = true
		}
	}
	left := make([]finding.Finding, 0, len(a.plan.Findings))
	for _, f := range a.plan.Findings {
		if !done[f.Path] {
			left = append(left, f)
		}
	}
	return left
}

// exitCode is the run's own code: its unresolved findings, with a group
// Apply could not land forced to the alarm code, because there is no
// finding class for "this could not be written".
func exitCode(a archive) int {
	code := finding.ExitCode(remaining(a, false))
	if a.result != nil && len(a.result.Failed) > 0 {
		code = finding.ExitAlarm
	}
	return code
}

// failedPaths are the files of the groups Apply could not land.
func failedPaths(a archive) map[string]bool {
	if a.result == nil || len(a.result.Failed) == 0 {
		return nil
	}
	failed := make(map[string]bool, len(a.result.Failed))
	for _, f := range a.result.Failed {
		failed[f.Key] = true
	}
	paths := make(map[string]bool)
	for _, group := range a.plan.Groups {
		if !failed[group.Key] {
			continue
		}
		for _, action := range group.Actions {
			paths[action.Old] = true
		}
	}
	return paths
}

// provenanceText is where a layout came from, in the words a report
// prints after the pattern itself.
func provenanceText(res layout.Resolution) string {
	switch res.Source {
	case layout.SourceFlag:
		return "from --layout"
	case layout.SourceDefault:
		return "from the built-in default"
	case layout.SourceConfig:
		return "from the global config " + res.SourcePath
	case "":
		return "from nowhere"
	default:
		return "from " + res.Source
	}
}

// human renders a run for a person.
type human struct {
	out   *out
	color palette
	prog  *progressLine
	// begun says whether an archive has already been reported, which is
	// what puts a blank line between two of them and not before the
	// first.
	begun bool
}

func newHuman(stdout, stderr *out, color palette, stderrTTY bool) *human {
	return &human{out: stdout, color: color, prog: &progressLine{out: stderr, on: stderrTTY}}
}

// head states the layout provenance before the work begins. The mode is
// not printed: the command line said what it was, and a report repeats
// evidence rather than the question.
func (h *human) head(_ engine.Mode, root string, nested bool, res layout.Resolution) {
	h.prog.clear()
	if h.begun {
		h.out.line("")
	}
	h.begun = true
	if nested {
		h.out.line(h.color.bold(fmt.Sprintf("archive %s (nested)", root)))
	}
	h.out.line(fmt.Sprintf("layout: %s (%s)",
		quotePattern(res.Pattern.String()), provenanceText(res)))
}

func (h *human) progress(phase engine.Phase, done, total int, path string) {
	h.prog.emit(phase, done, total, path)
}

func (h *human) body(a archive) {
	h.prog.clear()
	for _, sec := range sections(a) {
		h.out.line("")
		header := fmt.Sprintf("%s (%d):", sec.title, len(sec.entries))
		if sec.alarm {
			header = h.color.red(header)
		}
		h.out.line(header)
		for _, line := range sec.entries {
			h.out.line("  " + h.headline(sec.alarm, line))
			// The arrow is the whole evidence for a file that is simply
			// going somewhere; a file that is going nowhere, and damage
			// under any circumstances, has to say why.
			if line.detail != "" && (sec.evidence || line.to == "") {
				h.out.line("    " + line.detail)
			}
		}
	}
	h.failures(a)
	h.out.line("")
	h.out.line(summaryLine(a))
	for _, line := range resultLines(a) {
		h.out.line(line)
	}
	if line := skippedLine(a.skipped); line != "" {
		h.out.line(line)
	}
	for _, note := range a.notes {
		h.out.line("")
		h.out.line(note)
	}
}

// headline is one entry's line. The arrow is ASCII deliberately: a
// report is read on whatever console the machine has, and a character
// the console cannot draw is a character that hides a path.
func (h *human) headline(alarm bool, line entry) string {
	text := line.from
	if line.to != "" {
		text += " -> " + line.to
	}
	if alarm {
		return h.color.red(text)
	}
	return text
}

// failures reports the groups Apply could not land. They are not
// findings — there is no class for "this could not be written" — so they
// are printed on their own, prominently, and they are what forces the
// alarm exit code.
func (h *human) failures(a archive) {
	if a.result == nil || len(a.result.Failed) == 0 {
		return
	}
	h.out.line("")
	h.out.line(h.color.red(fmt.Sprintf("failed (%d):", len(a.result.Failed))))
	for _, f := range a.result.Failed {
		h.out.line("  " + h.color.red(f.Path))
		h.out.line("    " + f.Err.Error())
		if !f.Reverted {
			h.out.line("    " + h.color.red(
				"this group could not be put back, and is left part-applied;"+
					" re-running the same command completes it"))
		}
	}
}

// summaryLine is the one line a reader who reads nothing else reads.
func summaryLine(a archive) string {
	counts := make([]string, 0, len(classOrder))
	for _, class := range classOrder {
		if n := a.plan.Counts[class]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, class))
		}
	}
	text := fmt.Sprintf("%s, %d examined", count(len(a.plan.Groups), "group"), len(a.plan.Findings))
	if len(counts) > 0 {
		text += ": " + strings.Join(counts, ", ")
	}
	return text
}

// resultLines are what a mutation left behind: the files that landed,
// the receipt that recorded them, the marker that declares the archive
// from now on.
func resultLines(a archive) []string {
	var lines []string
	if a.unwritten != "" {
		lines = append(lines, a.unwritten)
	}
	if a.result == nil || a.result.Members == 0 {
		// A run that wrote nothing says so by having nothing to report
		// here; the counts above already said there was nothing to do.
		return lines
	}
	applied := "applied " + count(a.result.Members, "file")
	if a.result.Receipt != "" {
		applied += ", recorded in " + a.result.Receipt
	}
	lines = append(lines, applied)
	if a.result.Marker.Written {
		lines = append(lines, fmt.Sprintf("declared %s = %q in %s",
			layout.KeyLayout, a.result.Marker.Pattern, a.result.Marker.Path))
	}
	return lines
}

// count renders "1 file" and "2 files". A report that says "1 files" is
// a report a reader starts checking rather than trusting.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// skippedLine reports what recursion filtered away. None of it is a
// finding, and all of it is worth a line: an archive that appears to
// hold nothing and one whose files were all filtered away must not read
// alike.
func skippedLine(skipped scanner.Skipped) string {
	var parts []string
	if skipped.Hidden > 0 {
		parts = append(parts, fmt.Sprintf("%d hidden", skipped.Hidden))
	}
	if skipped.Other > 0 {
		parts = append(parts, fmt.Sprintf("%d in formats stampla does not name", skipped.Other))
	}
	if len(parts) == 0 {
		return ""
	}
	return "skipped " + strings.Join(parts, ", ")
}

// tail takes the progress line down. The outcome itself needs no line
// of its own: the exit code carries it, and the report already said
// what happened.
func (h *human) tail(_ outcome) { h.prog.clear() }
