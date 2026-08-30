package scanner

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestCollectGroups is the contract of group expansion: what belongs
// together, what the scan pulls in off disk, and in what order.
func TestCollectGroups(t *testing.T) {
	// One photo group as a card holds it, plus an untouched neighbor.
	card := []string{
		"card/DSC_1234.NEF",
		"card/DSC_1234.xmp",
		"card/DSC_1234.NEF.xmp",
		"card/NKSC_PARAM/DSC_1234.NEF.nksc",
		"card/DSC_9999.NEF",
	}
	// One named group, its edit, its vendor sidecar, and a member that
	// lives in another directory entirely — named files group by prefix,
	// with no directory logic at all.
	named := []string{
		"2022/2022-05/20220523_192742_d3147a94.nef",
		"2022/2022-05/20220523_192742_d3147a94-Edit.tif",
		"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc",
		"elsewhere/20220523_192742_d3147a94.jpg",
	}

	cases := []struct {
		name    string
		entries []string
		inputs  []string // slash-relative; none means the whole tree
		want    []string
	}{
		{
			// Selecting the master pulls in every sidecar beside it,
			// including the one parked in a vendor subdirectory.
			name:    "sidecars are pulled in",
			entries: card,
			inputs:  []string{"card/DSC_1234.NEF"},
			want: []string{
				"card/DSC_1234: card/DSC_1234.NEF card/DSC_1234.NEF.xmp~ " +
					"card/DSC_1234.xmp~ card/NKSC_PARAM/DSC_1234.NEF.nksc~",
			},
		},
		{
			// Selecting any member selects the group, from either end.
			name:    "a vendor sidecar selects its master",
			entries: card,
			inputs:  []string{"card/NKSC_PARAM/DSC_1234.NEF.nksc"},
			want: []string{
				"card/DSC_1234: card/DSC_1234.NEF~ card/DSC_1234.NEF.xmp~ " +
					"card/DSC_1234.xmp~ card/NKSC_PARAM/DSC_1234.NEF.nksc",
			},
		},
		{
			name:    "a sidecar beside its master",
			entries: card,
			inputs:  []string{"card/DSC_1234.NEF.xmp"},
			want: []string{
				"card/DSC_1234: card/DSC_1234.NEF~ card/DSC_1234.NEF.xmp " +
					"card/DSC_1234.xmp~ card/NKSC_PARAM/DSC_1234.NEF.nksc~",
			},
		},
		{
			// Files not yet named group per directory: two cards holding
			// DSC_1234 are two photos.
			name: "unnamed groups are per directory",
			entries: []string{
				"card/100ND780/DSC_1234.NEF",
				"card/101ND780/DSC_1234.NEF",
			},
			want: []string{
				"card/100ND780/DSC_1234: card/100ND780/DSC_1234.NEF",
				"card/101ND780/DSC_1234: card/101ND780/DSC_1234.NEF",
			},
		},
		{
			// A prefix embeds a content hash, so it is unique per master
			// across the archive: members group by it wherever they sit.
			name:    "named files group across directories",
			entries: named,
			want: []string{
				"20220523_192742_d3147a94: " +
					"2022/2022-05/20220523_192742_d3147a94.nef " +
					"elsewhere/20220523_192742_d3147a94.jpg " +
					"2022/2022-05/20220523_192742_d3147a94-Edit.tif " +
					"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc",
			},
		},
		{
			// Pull-in reads the neighborhood, not the whole archive: a
			// member in a directory the scan never visited stays out.
			name:    "pull-in reaches the neighborhood",
			entries: named,
			inputs:  []string{"2022/2022-05/20220523_192742_d3147a94.nef"},
			want: []string{
				"20220523_192742_d3147a94: " +
					"2022/2022-05/20220523_192742_d3147a94.nef " +
					"2022/2022-05/20220523_192742_d3147a94-Edit.tif~ " +
					"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc~",
			},
		},
		{
			// The labeled-derivative merge: the edit has no master of its
			// own, the shorter base does, so they converge together.
			name:    "a labeled derivative merges into its master",
			entries: []string{"card/IMG.NEF", "card/IMG-Edit.tif"},
			want:    []string{"card/IMG: card/IMG.NEF card/IMG-Edit.tif"},
		},
		{
			// And from the other end: selecting the edit selects the photo.
			name:    "selecting the derivative pulls in the master",
			entries: []string{"card/IMG.NEF", "card/IMG-Edit.tif"},
			inputs:  []string{"card/IMG-Edit.tif"},
			want:    []string{"card/IMG: card/IMG.NEF~ card/IMG-Edit.tif"},
		},
		{
			// A labeled group with a camera-native master is a separate
			// photo, not an edit — the camera numbered it that way.
			name:    "a labeled group with its own master stays separate",
			entries: []string{"card/IMG.NEF", "card/IMG_01.NEF"},
			want: []string{
				"card/IMG: card/IMG.NEF",
				"card/IMG_01: card/IMG_01.NEF",
			},
		},
		{
			name: "a labeled group keeps its own sidecars",
			entries: []string{
				"card/IMG.NEF", "card/IMG-Edit.NEF", "card/IMG-Edit.xmp",
			},
			want: []string{
				"card/IMG: card/IMG.NEF",
				"card/IMG-Edit: card/IMG-Edit.NEF card/IMG-Edit.xmp",
			},
		},
		{
			// Both DSC and DSC_a qualify as parents of DSC_a-Edit; the
			// longest wins, and DSC_a keeps its own master.
			name: "the longest qualifying parent wins",
			entries: []string{
				"card/DSC.NEF", "card/DSC_a.NEF", "card/DSC_a-Edit.tif",
			},
			want: []string{
				"card/DSC: card/DSC.NEF",
				"card/DSC_a: card/DSC_a.NEF card/DSC_a-Edit.tif",
			},
		},
		{
			// Nothing to be a derivative of: the shorter group owns no
			// master, so the labeled one is a group of its own.
			name:    "no merge into a group without a master",
			entries: []string{"card/IMG.xmp", "card/IMG-Edit.tif"},
			want: []string{
				"card/IMG: card/IMG.xmp",
				"card/IMG-Edit: card/IMG-Edit.tif",
			},
		},
		{
			// tif masters a group of its own, but never one that a
			// labeled group may merge into: an edit is not a photo.
			name:    "an editable master does not adopt a label",
			entries: []string{"card/SCAN.tif", "card/SCAN-Edit.tif"},
			want: []string{
				"card/SCAN: card/SCAN.tif",
				"card/SCAN-Edit: card/SCAN-Edit.tif",
			},
		},
		{
			// Only "-" and "_" label a derivative; a longer base name
			// that merely starts the same way is another photo.
			name:    "an unlabeled extension of a base is not a derivative",
			entries: []string{"card/IMG.NEF", "card/IMGX.tif"},
			want: []string{
				"card/IMG: card/IMG.NEF",
				"card/IMGX: card/IMGX.tif",
			},
		},
		{
			name:    "the merge does not cross directories",
			entries: []string{"card/IMG.NEF", "card/sub/IMG-Edit.tif"},
			want: []string{
				"card/IMG: card/IMG.NEF",
				"card/sub/IMG-Edit: card/sub/IMG-Edit.tif",
			},
		},
		{
			// A merged derivative brings its own sidecars along.
			name: "a merged derivative keeps its sidecar",
			entries: []string{
				"card/DSC1234.NEF", "card/DSC1234-Edit.tif", "card/DSC1234-Edit.tif.xmp",
			},
			want: []string{
				"card/DSC1234: card/DSC1234.NEF card/DSC1234-Edit.tif " +
					"card/DSC1234-Edit.tif.xmp",
			},
		},
		{
			// Master first, then the master-capable formats an editor
			// wrote, then sidecars — by path within each rank.
			name: "members are ordered master first",
			entries: []string{
				"card/DSC_1234.xmp",
				"card/DSC_1234.NEF.xmp",
				"card/DSC_1234-Edit.jpg",
				"card/DSC_1234.NEF",
				"card/DSC_1234.MOV",
			},
			want: []string{
				"card/DSC_1234: card/DSC_1234.MOV card/DSC_1234.NEF " +
					"card/DSC_1234-Edit.jpg card/DSC_1234.NEF.xmp card/DSC_1234.xmp",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := build(t, tc.entries...)
			inputs := []string{root}
			if len(tc.inputs) > 0 {
				inputs = under(root, tc.inputs...)
			}
			scan := collect(t, inputs, Options{})
			if len(scan.Errors) != 0 {
				t.Fatalf("findings = %v, want none", scan.Errors)
			}
			if got := summary(root, scan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("groups =\n\t%v\nwant\n\t%v", got, tc.want)
			}
		})
	}
}

func TestHomeDir(t *testing.T) {
	// Where a file's group lives: its own directory, or the master's
	// when a vendor tool parked it one level down.
	cases := []struct {
		path string
		want string
	}{
		{"card/DSC_1234.NEF", "card"},
		{"card/DSC_1234.NEF.xmp", "card"},
		{"card/NKSC_PARAM/DSC_1234.NEF.nksc", "card"},
		{"card/nksc_param/DSC_1234.NEF.NKSC", "card"},
		{"card/NKSC_PARAM/notes.xmp", "card/NKSC_PARAM"},
		{"2022/2022-05/20220523_192742_d3147a94.nef", "2022/2022-05"},
		{"2022/2022-05/20220523_192742_d3147a94-Edit.tif", "2022/2022-05"},
		{"2022/2022-05/NKSC_PARAM/20220523_192742_d3147a94.nef.nksc", "2022/2022-05"},
		{"DSC_1234.NEF", "."},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := homeDir(filepath.FromSlash(tc.path))
			if want := filepath.FromSlash(tc.want); got != want {
				t.Errorf("homeDir(%q) = %q, want %q", tc.path, got, want)
			}
		})
	}
}
