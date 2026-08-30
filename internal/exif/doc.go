// Package exif drives ExifTool, the only external tool stampla relies
// on.
//
// ExifTool runs as persistent -stay_open processes so that querying
// thousands of files never pays its startup cost per file; bulk reads
// shard the file list across a small pool of them. Metadata is always
// requested with -a and tag-group-qualified names: several tags with
// the same short name routinely coexist in one file, and only the
// group name tells them apart. Every read also requests the
// image-data hash.
//
// Usage:
//
//	if err := exif.Available(); err != nil {
//		return err // names the per-OS install hint
//	}
//	pool, err := exif.NewPool(0)
//	if err != nil {
//		return err
//	}
//	defer pool.Close()
//	for _, md := range pool.Read(paths, []string{"DateTimeOriginal", "CreateDate"}) {
//		...
//	}
//
// # Reads
//
// Read returns one Metadata per input path, in order, with failures
// reported per file rather than aborting the batch. It never writes or
// modifies anything: the argument list carries no write option, and
// every process is started with only read arguments.
//
// A relative path is resolved by the process that reads it, which
// inherited the working directory the pool was created in. Callers
// that may change directory should pass absolute paths.
//
// A Pool is safe for concurrent use; one process serves one chunk of
// one read at a time. Close waits for the reads in flight, and every
// read after it fails with ErrClosed. A process that stops answering
// is abandoned rather than waited on forever: the files of that chunk
// come back with the error, and the rest of the batch is unaffected.
//
// # Availability
//
// Available proves what a version number cannot. ImageDataHash is not
// extracted by default, and an unknown -api imagehashtype is only a
// warning, so Available reads the image-data hash of a fixture whose
// digest is known and compares it. A different digest is refused: it
// would mean every identity this build named disagreed with the ones
// named anywhere else.
//
// # Tags
//
// Read asks for the tags it is given, and nothing else. A whole-file
// dump is multi-KB for any file carrying an XMP edit history, and an
// archive-sized run pays that in pipe throughput and memory for data
// nothing reads. An empty list falls back to every tag, which is a
// convenience for exploring one file rather than a way to scan many.
//
// A bare name ("DateTimeOriginal") returns that tag from every group
// it appears in — what ranking wants, since it is the group that
// decides which of two identically named times to believe. A
// qualified name ("EXIF:DateTimeOriginal", "XMP:all") narrows it. The
// image-data hash, Error and Warning are always requested on top of
// the list: Error and Warning are tags too, and a file ExifTool could
// not parse comes back as an empty result rather than a failure
// unless they are asked for.
//
// Returned keys are ExifTool's family-0 groups — EXIF, MakerNotes,
// QuickTime, XMP, File, Composite — which are the coarse names
// capture-time resolution ranks by. When one group holds the same tag
// twice, the last value read wins. SourceFile is not a tag and is
// dropped from the map; it is Metadata.Path. A tag that is absent
// from a file is simply absent from the map.
//
// # The image-data hash
//
// ImageDataHash covers the image or video payload alone, so writing
// metadata never changes it while editing pixels does — the property
// the identity scheme rests on. Formats with no hashable payload
// (sidecars, plain files) return an empty hash, which is not an error.
//
// The hash type is pinned to MD5, and is not configurable: an
// identity must be reproducible on any machine, and a hash taken under
// another algorithm would be indistinguishable from a corrupted
// payload. XXH64 is not one of ExifTool's image hash types; MD5,
// SHA256 and SHA512 are.
//
// # Refusals
//
// The -stay_open protocol is one argument per line, and ExifTool reads
// any argument beginning with "-" as an option. Both are injection
// surfaces that a file name alone can reach, so a path containing a
// newline or carriage return is refused with a per-file error and
// never written to a process, and a relative path beginning with "-"
// is sent explicitly prefixed with "./".
//
// A tag name is the one argument a caller composes, and it reaches
// ExifTool as "-Name": a name carrying "=" would arrive as a tag
// assignment, which is a write. One unusable name refuses the whole
// read with ErrBadTag rather than sending any of it.
package exif
