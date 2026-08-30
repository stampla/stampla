// Package scanner collects the files a run operates on and expands
// them into convergence groups.
//
// Inputs: explicit files are taken literally, directories recurse,
// and --stdin supplies a newline- (or with -z, NUL-) delimited list.
// Scan errors are findings, not silent skips: an unreadable directory
// must surface — a silently missing file is the failure mode the tool
// exists to prevent.
//
// Group atomicity beats literal selection: selecting any member of a
// group (a master, its sidecars, labeled derivatives sharing its base
// name) pulls the whole group in, by examining siblings on disk.
//
// Mutation scans stop at nested archive roots (a directory with its
// own .stampla layout marker is not this run's business); the verify
// verb descends instead, and reports each nested root so the caller
// can recurse with that root's own resolution.
//
// Required API:
//
//	type Item struct {
//	    Path string
//	    Size int64
//	}
//
//	type Group struct {
//	    Key     string // identity.GroupKey of the members
//	    Members []Item // master first, then sidecars/derivatives
//	}
//
//	type Options struct {
//	    Stdin       io.Reader // non-nil: read the input list from here
//	    NulSep      bool      // -z
//	    StopAtRoots bool      // true for cp/mv, false for verify
//	}
//
//	type Scan struct {
//	    Groups      []Group
//	    NestedRoots []string          // markers encountered (verify recurses)
//	    Errors      []finding.Finding // scan-error findings
//	}
//
//	func Collect(inputs []string, opts Options) (*Scan, error)
package scanner
