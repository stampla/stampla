// Package cli is stampla's command-line interface: the layer that turns
// one command line into one run of the convergence engine, and the
// engine's plan into a report a person or a program can read.
//
// It owns no policy of its own. Identity, placement, classification and
// durability are decided beneath it. What this package decides is which
// question was asked, whether the destination is a destination, whether
// an unusual shape of run needs confirming, and how the answer is
// spoken.
//
// # Surface
//
//	stampla cp [-n] [-y] [--layout P] [--stdin [-z]] [--porcelain] [--color=MODE] [--workers N] <inputs...> <dest>
//	stampla mv [-n] [-y] [--layout P] [--stdin [-z]] [--porcelain] [--color=MODE] [--workers N] <inputs...> <dest>
//	stampla verify [--porcelain] [--color=MODE] [--workers N] <src> <dest>
//	stampla verify [--porcelain] [--color=MODE] [--workers N] <dest>
//	stampla version
//	stampla help [verb]
//
// cp and mv act — their names promise it — so there is no --apply and
// no --commit. The -n preview is the same plan with engine.Apply not
// called, which is what makes a preview unable to disagree with the run
// it previews. verify never mutates and takes neither -n nor --layout:
// what governs an archive is what the archive declares.
//
// --layout distinguishes an absent flag from --layout "" (the flat
// layout), because those are two different questions to the layout
// chain. With --stdin the sole positional is the destination and the
// file list is read from standard input (-z: NUL-delimited, for
// find -print0). "--" ends option parsing. There is no pager.
//
// # Guardrails
//
// Before any work, in this order:
//
//   - the shape of the command line — a missing destination, a --stdin
//     that has no pipe behind it, -z without --stdin — is a usage error
//     (64);
//   - the destination must be an existing directory (2). The message for
//     a destination that exists but is not a directory names the last
//     argument, because the usual cause is a glob that ate it;
//   - a container (layout-for-children and no layout of its own) is
//     refused, naming it (2);
//   - for cp and mv only, a destination inside another archive is
//     refused, naming the root to use instead (2). verify allows it
//     deliberately: verifying a subtree is a legitimate read-only
//     question, and answering it files nothing anywhere;
//   - ExifTool must be present and usable (2), checked before a pool is
//     started so the install hint arrives instead of a process failure.
//
// The rule behind the two codes: the shape of the command line is 64,
// the state of the world is 2.
//
// # Confirmations
//
// Four fixed predicates gate a mutation. They are evaluated together
// before anything is applied, each prints why it fired with its
// evidence, and each asks. -y is the standing yes; -n asks nothing —
// there is nothing to confirm about a run that writes nothing — and
// prints what it would have asked instead.
//
//  1. an explicit --layout contradicts the destination's own marker;
//  2. the plan would rename or relocate more than 100 files and more
//     than half of what is already under the root;
//  3. mv with a digital asset manager's catalog beside the destination;
//  4. mv whose sources sit on removable media.
//
// Prompts are written to stderr and read from the controlling terminal
// — /dev/tty, or CONIN$ on Windows — never from stdin, which may be the
// --stdin file list. Without a terminal the run is refused with a hint
// to pass -y. Declined and refused are the same exit code (3): both mean
// the plan was not applied because a question was not answered yes.
//
// # Output
//
// The report goes to stdout, everything else — usage, refusals,
// progress, prompts — to stderr. Progress is a single line rewritten in
// place, and only when stderr is a terminal; under --porcelain it is
// events in the stream instead.
//
// A human report states its layout provenance first, then what a run
// that acted actually did — taken from what landed, in the order it
// landed — then what it did not resolve, grouped by class with
// old -> new lines, alarms first and in red. Converged files are counted
// rather than listed: an archive that verifies clean has nothing to say
// about twenty thousand files one at a time.
//
// # Porcelain
//
// --porcelain writes NDJSON on stdout, one object per line. This is the
// contract the desktop app speaks, so the field names are fixed:
//
//	{"type":"plan","format":1,"mode":"cp","dest":"/photos","layout":"{yyyy}/{yyyy}-{mm}","source":"/photos/.stampla"}
//	{"type":"finding","class":"incoming","path":"/card/DSC.NEF","old":"/card/DSC.NEF","new":"/photos/2026/2026-07/20260703_150727_9b677b64.nef","detail":"named …"}
//	{"type":"progress","phase":"apply","done":3,"total":12}
//	{"type":"result","exit":0,"applied":12,"failed":0,"receipt":"/photos/.stampla.log","marker":"/photos/.stampla"}
//
// What a consumer may rely on:
//
//   - format is 1 and appears on the plan object, which is always the
//     first line. A consumer that does not know a format refuses the
//     stream rather than guessing at it.
//   - mode is engine.Mode's own string: cp, mv, verify-membership,
//     verify-self.
//   - class is finding.Class's own string, so the vocabulary of a
//     machine reader and of a report are one vocabulary.
//   - every field of every object is always present, empty rather than
//     absent, so a consumer never has to distinguish the two.
//   - a stream carries one plan object per archive examined:
//     verify <dest> emits one for the destination and one for every
//     nested root it descends into.
//   - exactly one result object, and it is the last line — including
//     when the run ends early, so a failed stream never reads as a
//     truncated one. A run refused before it starts (a destination that
//     is not a directory, no ExifTool) writes no stream at all: the exit
//     code and stderr are the whole answer.
//
// Two things format 1 deliberately does not carry: the prose hints a
// human report prints, and per-file detail of an apply failure — the
// result object's failed count is what says a group did not land.
//
// # Exit codes
//
//	0  converged, verified, nothing to do
//	1  findings that need action (stale, misplaced, missing, unresolvable, conflict)
//	2  alarms (corrupt, time-drift), operational trouble, refusals
//	3  a confirmation was declined or could not be asked
//	64 usage
//
// The code is finding.ExitCode over the findings the run did not
// resolve. Work that landed is not pending any more, which is why an
// import that worked exits 0 while its own preview exits 1: the
// preview's findings are all still true. Two overrides: a non-empty
// engine.Result.Failed reports at 2 whatever the findings said, because
// there is no finding class for "this could not be written"; and the
// refusals above report at their own code. Where a run examines several
// archives, the worst code wins.
//
// verify <src> <dest> exiting 0 means exactly that every source file was
// accounted for at its place in the destination archive — the card is
// safe to format — which is why a scan that could not read part of the
// source is a finding rather than a silence.
//
// # Decisions the design left open
//
//   - --color takes a value (--color=never or --color never). A bare
//     --color would have to swallow the next argument or refuse to, and
//     one of those two behaviors would eventually eat a destination.
//   - A destination that does not exist, and one that exists and is not
//     a directory, are both operational refusals (2) rather than usage
//     errors: the command line was well formed and the world disagreed.
//   - verify of a directory that is not an archive root — a subtree of
//     one, say — resolves no declared layout, so it checks names and
//     content and leaves placement alone. Judging a subtree against a
//     pattern rooted at that subtree would call every correctly filed
//     photograph misplaced.
//   - A verify that descends stops each scan where the next archive
//     begins and verifies that archive next, under its own declaration.
//     Every file is classified exactly once, by the layout that governs
//     it.
//   - Report lines use "->" rather than an arrow glyph: a report is read
//     on whatever console the machine has, and a character the console
//     cannot draw is a character that hides a path.
package cli
