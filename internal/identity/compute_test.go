package identity

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/exif"
)

func TestCompute(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want string
		hash HashSource
		tag  string
	}{
		{
			name: "raw master from EXIF and the image-data hash",
			in: Input{
				Path:          "card/DSC_1234.NEF",
				Tags:          nefTags,
				ImageDataHash: "1355acb2beefcafe",
			},
			want: "20240601_132110_1355acb2.nef", hash: HashImage,
			tag: "EXIF:DateTimeOriginal",
		},
		{
			name: "video takes the video chain",
			in: Input{
				Path:          "card/A001.MOV",
				Tags:          movTags,
				ImageDataHash: "941930e900000000",
			},
			want: "20250615_160910_941930e9.mov", hash: HashImage,
			tag: "MakerNotes:DateTimeOriginal",
		},
		{
			name: "braw keeps the local QuickTime reading",
			in: Input{
				Path:          "card/A001.braw",
				Tags:          brawTags,
				ImageDataHash: "941930e9deadbeef",
			},
			want: "20210808_145653_941930e9.braw", hash: HashImage,
			tag: "QuickTime:CreateDate",
		},
		{
			name: "whole-file digest fills in where the payload cannot be hashed",
			in: Input{
				Path:     "card/DSC_1234.nef",
				Tags:     nefTags,
				FileHash: "0011223344556677",
			},
			want: "20240601_132110_00112233.nef", hash: HashFile,
			tag: "EXIF:DateTimeOriginal",
		},
		{
			name: "the image-data hash wins when both are present",
			in: Input{
				Path:          "card/DSC_1234.nef",
				Tags:          nefTags,
				ImageDataHash: "aabbccddeeff0011",
				FileHash:      "0011223344556677",
			},
			want: "20240601_132110_aabbccdd.nef", hash: HashImage,
			tag: "EXIF:DateTimeOriginal",
		},
		{
			name: "an uppercase digest is folded, like the extension",
			in: Input{
				Path:          "card/DSC_1234.NEF",
				Tags:          nefTags,
				ImageDataHash: "AABBCCDDEEFF0011",
			},
			want: "20240601_132110_aabbccdd.nef", hash: HashImage,
			tag: "EXIF:DateTimeOriginal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Path = filepath.FromSlash(tc.in.Path)
			id, provenance, err := ComputeFrom(tc.in)
			if err != nil {
				t.Fatalf("ComputeFrom: %v", err)
			}
			if got := id.Name(); got != tc.want {
				t.Errorf("Name() = %q, want %q", got, tc.want)
			}
			if provenance.Hash != tc.hash || provenance.TimeTag != tc.tag {
				t.Errorf("provenance = %+v, want %s from %s", provenance, tc.hash, tc.tag)
			}
		})
	}
}

func TestComputeFromMetadata(t *testing.T) {
	md := exif.Metadata{
		Path:          filepath.FromSlash("card/DSC_1234.NEF"),
		Tags:          nefTags,
		ImageDataHash: "1355acb2beefcafe",
	}
	id, provenance, err := Compute(md, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got := id.Name(); got != "20240601_132110_1355acb2.nef" {
		t.Errorf("Name() = %q", got)
	}
	if provenance.Hash != HashImage || provenance.TimeTag != "EXIF:DateTimeOriginal" {
		t.Errorf("provenance = %+v", provenance)
	}

	// A format ExifTool cannot hash the payload of falls back to the
	// caller's whole-file digest, and the report says so.
	md.ImageDataHash = ""
	if _, provenance, err = Compute(md, "0011223344556677"); err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if provenance.Hash != HashFile {
		t.Errorf("provenance.Hash = %q, want %q", provenance.Hash, HashFile)
	}

	// A read that failed is never named around.
	readErr := errors.New("exiftool said no")
	if _, _, err = Compute(exif.Metadata{Path: md.Path, Err: readErr}, ""); !errors.Is(err, readErr) {
		t.Errorf("Compute dropped the read error: %v", err)
	}
}

func TestComputeRefusals(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want error
	}{
		{
			name: "a sidecar has no identity of its own",
			in: Input{
				Path: "card/DSC_1234.xmp", Tags: xmpSidecarTags,
				ImageDataHash: "1355acb2beefcafe",
			},
			want: ErrNotMedia,
		},
		{
			name: "an extensionless file is not media",
			in:   Input{Path: "card/DSC_1234", Tags: nefTags, ImageDataHash: "1355acb2"},
			want: ErrNotMedia,
		},
		{
			name: "no hash at all: half an identity is not one",
			in:   Input{Path: "card/DSC_1234.nef", Tags: nefTags},
			want: ErrNoContentHash,
		},
		{
			name: "a hash shorter than the slice a name carries",
			in: Input{
				Path: "card/DSC_1234.nef", Tags: nefTags,
				ImageDataHash: "1355ac",
			},
			want: ErrBadContentHash,
		},
		{
			name: "a digest that is not hexadecimal",
			in: Input{
				Path: "card/DSC_1234.nef", Tags: nefTags,
				ImageDataHash: "not-a-digest",
			},
			want: ErrBadContentHash,
		},
		{
			name: "no capture time: reported, never guessed",
			in: Input{
				Path:          "card/DSC_1234.nef",
				Tags:          map[string]string{"EXIF:Model": "NIKON Z 7_2"},
				ImageDataHash: "1355acb2beefcafe",
			},
			want: ErrNoCaptureTime,
		},
		{
			name: "a still with only maker-notes dates is unresolvable, not renamed",
			in: Input{
				Path: "card/DSC_1234.nef", Tags: movTags,
				ImageDataHash: "1355acb2beefcafe",
			},
			want: ErrNoCaptureTime,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Path = filepath.FromSlash(tc.in.Path)
			id, _, err := ComputeFrom(tc.in)
			if err == nil {
				t.Fatalf("ComputeFrom returned %+v, want an error", id)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error %v does not wrap %v", err, tc.want)
			}
			// Every refusal names the file it is about.
			if !strings.Contains(err.Error(), tc.in.Path) {
				t.Errorf("error %q does not name %q", err, tc.in.Path)
			}
		})
	}
}
