package identity

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stampla/stampla/internal/exif"
)

var (
	// ErrNotMedia reports that the file carries no identity of its own —
	// a sidecar takes its master's, and everything else has none.
	ErrNotMedia = errors.New("not a media file")
	// ErrNoContentHash reports that neither hash was supplied for a media
	// file. Half an identity is not an identity.
	ErrNoContentHash = errors.New("no content hash")
	// ErrBadContentHash reports a hash that is not hexadecimal, or is
	// shorter than the slice a name carries.
	ErrBadContentHash = errors.New("unusable content hash")
)

// HashSource says which digest a name's hash slice was cut from.
type HashSource string

const (
	// HashImage is ExifTool's ImageDataHash, taken over the image or
	// video payload only. Writing metadata never changes it; editing
	// pixels does.
	HashImage HashSource = "image"
	// HashFile is a whole-file digest, used for formats ExifTool has no
	// payload hash for. Metadata edits move it, so such a file's
	// identity drifts with any write.
	HashFile HashSource = "file"
)

// Input is everything computing one file's identity takes: an
// exif.Metadata plus the fallback digest, which comes from the caller
// because this package performs no I/O of its own.
type Input struct {
	// Path is the file the metadata was read from; only its extension
	// is consulted.
	Path string
	// Tags are tag-group-qualified metadata values ("EXIF:CreateDate").
	Tags map[string]string
	// ImageDataHash is ExifTool's payload-only digest, lowercase hex,
	// empty when the format does not support one.
	ImageDataHash string
	// FileHash is a whole-file digest, lowercase hex. It is used only
	// when ImageDataHash is empty, and the identity then drifts with
	// metadata writes — which is why the choice is reported.
	FileHash string
}

// Provenance is the evidence behind a computed identity: every report
// states where its answer came from.
type Provenance struct {
	// TimeTag is the winning tag-group-qualified tag name, carrying an
	// "@utc" marker when the value was converted from UTC.
	TimeTag string
	// Hash says which digest the hash slice was cut from.
	Hash HashSource
}

// Compute derives a file's identity from one ExifTool read. fileHash is
// the caller's whole-file digest, used only for a format ExifTool has
// no payload hash for; passing "" refuses such a file rather than
// naming it.
func Compute(md exif.Metadata, fileHash string) (Identity, Provenance, error) {
	if md.Err != nil {
		return Identity{}, Provenance{}, fmt.Errorf("%s: %w", md.Path, md.Err)
	}
	return ComputeFrom(Input{
		Path:          md.Path,
		Tags:          md.Tags,
		ImageDataHash: md.ImageDataHash,
		FileHash:      fileHash,
	})
}

// ComputeFrom derives a file's identity from its metadata: the capture
// time its format's chain ranks highest, the leading digits of its
// content hash, and its lowercased extension. It never guesses — an
// unresolvable capture time or a missing hash is an error, because a
// name that is wrong is worse than a file that keeps its old one.
func ComputeFrom(in Input) (Identity, Provenance, error) {
	ext := extOf(in.Path)
	if !isMediaExt(ext) {
		return Identity{}, Provenance{}, fmt.Errorf("%s: %w", in.Path, ErrNotMedia)
	}

	digest, source := in.ImageDataHash, HashImage
	if digest == "" {
		digest, source = in.FileHash, HashFile
	}
	if digest == "" {
		return Identity{}, Provenance{}, fmt.Errorf("%s: %w", in.Path, ErrNoContentHash)
	}
	slice, err := hashSlice(digest)
	if err != nil {
		return Identity{}, Provenance{}, fmt.Errorf("%s: %w", in.Path, err)
	}

	stamp, tag, err := ResolveWith(in.Tags, ChainFor(ext))
	if err != nil {
		return Identity{}, Provenance{}, fmt.Errorf("%s: %w", in.Path, err)
	}

	return Identity{Time: stamp, Hash: slice, Ext: ext},
		Provenance{TimeTag: tag, Hash: source}, nil
}

// hashSlice cuts the leading digits a name carries out of a full
// hexadecimal digest. The whole digest is checked, not just the slice:
// a garbled digest is evidence that the read went wrong, and a name
// built from its first eight characters would hide that.
func hashSlice(digest string) (string, error) {
	if len(digest) < HashLength {
		return "", fmt.Errorf("%w: %q is shorter than %d digits",
			ErrBadContentHash, digest, HashLength)
	}
	digest = strings.ToLower(digest)
	for i := range len(digest) {
		if !isHexDigit(digest[i]) {
			return "", fmt.Errorf("%w: %q is not hexadecimal", ErrBadContentHash, digest)
		}
	}
	return digest[:HashLength], nil
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
