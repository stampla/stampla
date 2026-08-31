// Package engine is the convergence engine: it works out where every
// selected file belongs under a destination root, and moves it there
// without ever destroying information.
//
// There is one operation — converge files onto their identities under a
// destination root — and four modes are entry points to it. BuildPlan
// examines, classifies and decides; Apply executes. Planning never
// mutates anything, not even lazily, which is what makes a dry run the
// absence of Apply rather than a second code path that might disagree
// with the first.
//
//	plan, err := engine.BuildPlan(engine.Options{
//		Mode: engine.Copy, Scan: scan, Dest: dest,
//		Resolution: resolution, Pool: pool,
//	})
//	if err != nil {
//		return err
//	}
//	if !dryRun {
//		result, err := engine.Apply(plan, engine.ApplyOptions{})
//		...
//	}
//
// # Classification
//
// Every examined file lands in exactly one finding.Class. A group's
// master carries the content evidence: its capture time and image-data
// hash are recomputed and compared with what its name claims, and every
// other member of the group inherits the master's prefix. The comparison
// distinguishes what a mismatch means, because renaming the wrong file
// destroys the only record of what it used to be:
//
//   - a hash mismatch on a write-once format (RAW, camera-original
//     video) is finding.Corrupt: an alarm, never renamed, under every
//     mode;
//   - a capture-time mismatch on a write-once format whose hash is
//     intact is finding.TimeDrift, also an alarm and also never renamed
//     — ImageDataHash does not cover metadata, so damage to a metadata
//     region must not pass as an innocent date edit;
//   - either mismatch on an editable format (JPEG, TIFF, DNG, HEIC and
//     sidecars) is finding.Stale, and renames;
//   - a name that is not canonical at all is finding.Incoming: there is
//     no identity in it to disagree with the file, so it is given one;
//   - a file with the right name in the wrong directory is
//     finding.Misplaced, and relocates only when the layout is declared;
//   - a file with no resolvable capture time makes its whole group
//     finding.Unresolvable — reported, skipped, never guessed at, and
//     never a reason to abort the run.
//
// A target path that is already occupied by a file whose name parses to
// the same identity is finding.Converged: the name embeds the content
// hash, so a file sitting under an identity name is that identity, and
// re-importing the same memory card converges instead of duplicating.
// A target occupied by anything else is finding.Conflict, and is
// refused.
//
// # Groups
//
// A group either fully converges or is reported. Apply works one group
// at a time and reverts on the spot when a member fails, so a master is
// never left split from its sidecars; later groups still proceed,
// because one unreadable file must not stop a card from being imported.
//
// # Durability
//
// There is no journal. Crash recovery is re-running the same command:
//
//   - every copy lands under a scratch name in the target directory and
//     is renamed into place only after its bytes have been read back and
//     verified, so a partial file never exists under an identity name;
//   - a rename claims its target with a hard link before dropping the
//     source, so an existing target fails atomically instead of being
//     silently replaced, and the half-second in which both names exist
//     is recognizable and completed by the next run;
//   - a leftover scratch file is cleaned up by the run that next needs
//     that target;
//   - every applied mutation appends a line to the receipt at the
//     destination root, fsynced after each group, so the receipts of an
//     interrupted run name exactly the groups that landed.
//
// A second run of the same command sees the landed members as converged
// and finishes the rest.
//
// # Two hashes
//
// The engine uses two digests for two different jobs, and they are not
// interchangeable. Identity is the ExifTool image-data hash: it covers
// the payload alone, so writing metadata never changes a file's name
// while editing pixels does. Transfer verification is a whole-file MD5
// computed while the bytes stream past and checked against the bytes as
// they landed: a copy must reproduce the file exactly, metadata
// included, so the payload-only digest would not notice a truncated or
// scrambled header. The whole-file digest also stands in as the identity
// hash for a format ExifTool has no payload hash for, which the
// resulting Provenance says out loud.
package engine
