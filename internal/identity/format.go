package identity

import (
	"path/filepath"
	"strings"
)

// The format tables. Every extension is bare and lowercase; callers may
// pass either form and are folded on the way in.

// rawExtensions are the camera RAW families, plus the two container
// formats a sidecar may append to (tif, tiff) and the RAW-like DNG.
// Membership here is about the *name* grammar, not about mutability:
// dng, tif and tiff are also editable, below.
var rawExtensions = []string{
	"arw", "cr2", "cr3", "dng", "nef", "raf", "rw2", "tif", "tiff",
}

// videoExtensions are the moving-image formats. All of them are
// camera-original: nothing edits a clip in place and keeps its name.
var videoExtensions = []string{
	"3gp", "asf", "avi", "braw", "m2ts", "m4v", "mkv", "mov",
	"mp4", "mpeg", "mpg", "mts", "nev", "r3d", "wmv",
}

// looseMasterExtensions can master a group without a camera RAW beside
// them: plain shots (JPEG, phone HEIC) and standalone DNGs.
var looseMasterExtensions = []string{"dng", "heic", "heif", "jpeg", "jpg"}

// editableExtensions are the master-capable formats that are edited in
// place — keywords, ratings, rename tokens, pixel edits. Their content
// drifts legitimately, so a mismatch renames instead of alarming.
// Everything else that can master a group is write-once.
var editableExtensions = []string{
	"dng", "heic", "heif", "jpeg", "jpg", "psd", "tif", "tiff",
}

// sidecarExtensions are vendor sidecars that appear without appending
// their master's extension. Sidecars that do append one (.nef.xmp,
// .rw2.pp3, .nef.dop) are recognized by shape instead.
var sidecarExtensions = []string{"nksc", "pp3", "xmp"}

// VendorSidecarDir describes sidecars a tool keeps in a subdirectory
// beside their masters: <dir>/<Subdir>/<master-name><Strip> belongs to
// <dir>/<master-name>.
type VendorSidecarDir struct {
	Subdir string
	Strip  string
}

// vendorSidecarDirs is the built-in list. Nikon NX Studio is the one
// tool in the wild that does this.
var vendorSidecarDirs = []VendorSidecarDir{{Subdir: "NKSC_PARAM", Strip: ".nksc"}}

var (
	rawSet      = setOf(rawExtensions)
	videoSet    = setOf(videoExtensions)
	looseSet    = setOf(looseMasterExtensions)
	editableSet = setOf(editableExtensions)
	sidecarSet  = setOf(sidecarExtensions)
)

func setOf(extensions []string) map[string]bool {
	set := make(map[string]bool, len(extensions))
	for _, extension := range extensions {
		set[extension] = true
	}
	return set
}

// normalizeExt folds an extension to the bare lowercase form the tables
// use. It accepts ".NEF", "NEF" and "nef" alike.
func normalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}

// extOf is the bare lowercase extension of a path.
func extOf(path string) string {
	return normalizeExt(filepath.Ext(filepath.Base(path)))
}

func isMediaExt(ext string) bool {
	return rawSet[ext] || videoSet[ext] || looseSet[ext]
}

func isVideoExt(ext string) bool { return videoSet[ext] }

// IsMedia reports whether the path names a photo or video stampla owns
// an identity for: a camera RAW, a still that can master a group, or a
// video. Sidecars and everything else are false.
func IsMedia(path string) bool {
	return isMediaExt(extOf(path))
}

// IsWriteOnce reports whether the extension names a format that is
// never renamed on a content mismatch: the RAW families and
// camera-original video. The old name is the only surviving record of
// what such a file's identity used to be, and renaming would convert
// damage into a plausible file. Editable formats (JPEG, TIFF, DNG,
// HEIC and sidecars) drift legitimately and rename.
func IsWriteOnce(ext string) bool {
	ext = normalizeExt(ext)
	return isMediaExt(ext) && !editableSet[ext]
}

// CameraNative reports whether the extension names a format a camera
// writes: the RAW families and video, but never the containers an
// editor writes into (tif, tiff, dng) even though those can master a
// group of their own.
//
// It serves the labeled-derivative merge described in the package docs,
// which asks whether a group owns a master before deciding that a
// labeled group beside it is a derivative. Editor output such as
// …-Edit.tif must answer no there, or every edit would look like a photo
// of its own.
func CameraNative(ext string) bool {
	ext = normalizeExt(ext)
	return (rawSet[ext] || videoSet[ext]) && !editableSet[ext]
}

// IsSidecar reports whether the path names a file that carries no
// identity of its own and takes its master's: an .xmp, a vendor sidecar
// (.nksc, .pp3), one kept in a vendor sidecar subdirectory, or any file
// whose name appends its own extension to a master's (DSC1234.nef.dop).
func IsSidecar(path string) bool {
	base := filepath.Base(path)
	ext := extOf(base)
	if ext == "" || isMediaExt(ext) {
		return false
	}
	if sidecarSet[ext] {
		return true
	}
	if _, _, ok := vendorSidecarHome(path); ok {
		return true
	}
	// Appended shape: the stem's own extension is a master's.
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return isMediaExt(extOf(stem))
}

// GroupKey is the key of the group a file converges with: a master and
// its sidecars and derivatives, which rename as one unit.
//
// Named files key on their prefix alone. Prefixes embed a content hash,
// so they are unique per master across the whole archive and a sidecar
// parked in a subdirectory needs no directory logic to find its way
// home. Files not yet named key on their directory and the base name
// before the first dot, after any vendor sidecar subdirectory has been
// resolved back to the master's directory — so DSC1234.NEF, DSC1234.xmp
// and NKSC_PARAM/DSC1234.NEF.nksc share one key. The labeled-derivative
// merge described in the package docs is applied by the caller over
// these keys.
func GroupKey(path string) string {
	base := filepath.Base(path)
	if prefix, ok := namedPrefix(base); ok {
		return prefix
	}
	dir := filepath.Dir(path)
	if home, name, ok := vendorSidecarHome(path); ok {
		dir, base = home, name
	}
	base, _, _ = strings.Cut(base, ".")
	return filepath.Join(dir, base)
}

// vendorSidecarHome resolves a vendor sidecar subdirectory: it reports
// the master's directory and the master's name for a file that matches
// one of the rules, and false for everything else. The directory name
// is matched case-insensitively — the same volume read on Windows and
// on Linux must group the same way.
func vendorSidecarHome(path string) (dir, name string, ok bool) {
	base := filepath.Base(path)
	parent := filepath.Dir(path)
	for _, rule := range vendorSidecarDirs {
		if !strings.EqualFold(filepath.Base(parent), rule.Subdir) {
			continue
		}
		if len(base) <= len(rule.Strip) ||
			!strings.EqualFold(base[len(base)-len(rule.Strip):], rule.Strip) {
			continue
		}
		return filepath.Dir(parent), base[:len(base)-len(rule.Strip)], true
	}
	return "", "", false
}
