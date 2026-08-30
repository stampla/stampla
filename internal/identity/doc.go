// Package identity computes and parses canonical names, resolves
// capture times, and owns all format knowledge: which extensions are
// media, which formats are write-once, and what makes a sidecar.
// Everything here is a pure function of its inputs — no I/O — so the
// hard rules are exhaustively table-testable.
//
// The canonical name is YYYYMMDD_HHMMSS_hhhhhhhh.ext: capture time in
// local wall-clock time, the first 8 hex digits of the ImageDataHash,
// and the lowercased original extension.
//
// Required API:
//
//	type Identity struct {
//	    Time  time.Time // local wall-clock capture time
//	    Hash  string    // 8 lowercase hex digits
//	    Ext   string    // lowercase, without dot
//	}
//	func (id Identity) Name() string
//	func ParseName(base string) (Identity, bool) // false: not a canonical name
//
//	// Resolve ranks tag-group-qualified capture-time candidates:
//	// maker/EXIF original-capture tags first, then format-appropriate
//	// creation tags; QuickTime UTC values are converted using the
//	// file's declared offset. It returns the winning tag name as
//	// provenance. No resolvable time → error (never a guess).
//	func Resolve(tags map[string]string) (t time.Time, source string, err error)
//
//	func Compute(md exif.Metadata) (Identity, error)
//
//	func IsMedia(path string) bool     // extension allowlist (RAW, image, video)
//	func IsWriteOnce(ext string) bool  // RAW + camera-original video
//	func IsSidecar(path string) bool   // .xmp and vendor sidecar shapes
//
//	// GroupKey groups files that must converge as one unit: named
//	// files by their name prefix, unnamed files by their base name
//	// (DSC_1234.NEF + DSC_1234.xmp + DSC_1234.jpg share a key).
//	func GroupKey(path string) string
package identity
