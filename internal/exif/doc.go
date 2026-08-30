// Package exif drives ExifTool, the only external tool stampla
// relies on.
//
// ExifTool runs as persistent -stay_open processes so that querying
// thousands of files never pays its startup cost per file; bulk reads
// shard the file list across a small pool of them. Metadata is always
// requested with -a and tag-group-qualified names: several tags with
// the same short name routinely coexist in one file, and only the
// group name tells them apart. Every read also requests the
// ImageDataHash (xxh64).
//
// Required API (implemented in this package):
//
//	type Metadata struct {
//	    Path          string
//	    Tags          map[string]string // "Group:TagName" → raw value
//	    ImageDataHash string            // lowercase hex; "" if unsupported
//	    Err           error             // per-file read failure
//	}
//
//	func Available() error                       // nil, or an error naming the per-OS install hint
//	func NewPool(size int) (*Pool, error)        // size <= 0 → default
//	func (p *Pool) Read(paths []string) []Metadata // order-preserving; per-file errors inline
//	func (p *Pool) Close() error
//
// Reads never write anything. Filenames containing newlines are
// refused with a per-file error rather than passed to the protocol.
package exif
