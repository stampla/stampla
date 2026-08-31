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
				"card/DSC_1234 (photo): card/DSC_1234.NEF card/DSC_1234.NEF.xmp~ " +
					"card/DSC_1234.xmp~ card/NKSC_PARAM/DSC_1234.NEF.nksc~",
			},
		},
		{
			// Selecting any member selects the group, from either end.
			name:    "a vendor sidecar selects its master",
			entries: card,
			inputs:  []string{"card/NKSC_PARAM/DSC_1234.NEF.nksc"},
			want: []string{
				"card/DSC_1234 (photo): card/DSC_1234.NEF~ card/DSC_1234.NEF.xmp~ " +
					"card/DSC_1234.xmp~ card/NKSC_PARAM/DSC_1234.NEF.nksc",
			},
		},
		{
			name:    "a sidecar beside its master",
			entries: card,
			inputs:  []string{"card/DSC_1234.NEF.xmp"},
			want: []string{
				"card/DSC_1234 (photo): card/DSC_1234.NEF~ card/DSC_1234.NEF.xmp " +
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
				"card/100ND780/DSC_1234 (photo): card/100ND780/DSC_1234.NEF",
				"card/101ND780/DSC_1234 (photo): card/101ND780/DSC_1234.NEF",
			},
		},
		{
			// A prefix embeds a content hash, so it is unique per master
			// across the archive: members group by it wherever they sit.
			name:    "named files group across directories",
			entries: named,
			want: []string{
				"20220523_192742_d3147a94 (photo): " +
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
				"20220523_192742_d3147a94 (photo): " +
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
			want:    []string{"card/IMG (photo): card/IMG.NEF card/IMG-Edit.tif"},
		},
		{
			// And from the other end: selecting the edit selects the photo.
			name:    "selecting the derivative pulls in the master",
			entries: []string{"card/IMG.NEF", "card/IMG-Edit.tif"},
			inputs:  []string{"card/IMG-Edit.tif"},
			want:    []string{"card/IMG (photo): card/IMG.NEF~ card/IMG-Edit.tif"},
		},
		{
			// A labeled group with a camera-native master is a separate
			// photo, not an edit — the camera numbered it that way.
			name:    "a labeled group with its own master stays separate",
			entries: []string{"card/IMG.NEF", "card/IMG_01.NEF"},
			want: []string{
				"card/IMG (photo): card/IMG.NEF",
				"card/IMG_01 (photo): card/IMG_01.NEF",
			},
		},
		{
			name: "a labeled group keeps its own sidecars",
			entries: []string{
				"card/IMG.NEF", "card/IMG-Edit.NEF", "card/IMG-Edit.xmp",
			},
			want: []string{
				"card/IMG (photo): card/IMG.NEF",
				"card/IMG-Edit (photo): card/IMG-Edit.NEF card/IMG-Edit.xmp",
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
				"card/DSC (photo): card/DSC.NEF",
				"card/DSC_a (photo): card/DSC_a.NEF card/DSC_a-Edit.tif",
			},
		},
		{
			// Nothing to be a derivative of: the shorter group owns no
			// master, so the labeled one is a group of its own.
			name:    "no merge into a group without a master",
			entries: []string{"card/IMG.xmp", "card/IMG-Edit.tif"},
			want: []string{
				"card/IMG (photo): card/IMG.xmp",
				"card/IMG-Edit (photo): card/IMG-Edit.tif",
			},
		},
		{
			// tif masters a group of its own, but never one that a
			// labeled group may merge into: an edit is not a photo.
			name:    "an editable master does not adopt a label",
			entries: []string{"card/SCAN.tif", "card/SCAN-Edit.tif"},
			want: []string{
				"card/SCAN (photo): card/SCAN.tif",
				"card/SCAN-Edit (photo): card/SCAN-Edit.tif",
			},
		},
		{
			// Only "-" and "_" label a derivative; a longer base name
			// that merely starts the same way is another photo.
			name:    "an unlabeled extension of a base is not a derivative",
			entries: []string{"card/IMG.NEF", "card/IMGX.tif"},
			want: []string{
				"card/IMG (photo): card/IMG.NEF",
				"card/IMGX (photo): card/IMGX.tif",
			},
		},
		{
			name:    "the merge does not cross directories",
			entries: []string{"card/IMG.NEF", "card/sub/IMG-Edit.tif"},
			want: []string{
				"card/IMG (photo): card/IMG.NEF",
				"card/sub/IMG-Edit (photo): card/sub/IMG-Edit.tif",
			},
		},
		{
			// A merged derivative brings its own sidecars along.
			name: "a merged derivative keeps its sidecar",
			entries: []string{
				"card/DSC1234.NEF", "card/DSC1234-Edit.tif", "card/DSC1234-Edit.tif.xmp",
			},
			want: []string{
				"card/DSC1234 (photo): card/DSC1234.NEF card/DSC1234-Edit.tif " +
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
			},
			want: []string{
				"card/DSC_1234 (photo): card/DSC_1234.NEF card/DSC_1234-Edit.jpg " +
					"card/DSC_1234.NEF.xmp card/DSC_1234.xmp",
			},
		},

		// The media boundary. A group never spans photo and video: one
		// name asserts one identity, and a still that took a clip's
		// prefix would claim a capture it never had — in a name no later
		// verify would question, since only masters are content-checked.
		{
			name:    "a photo and a video sharing a base are two groups",
			entries: []string{"card/DSC_1234.JPG", "card/DSC_1234.MP4"},
			want: []string{
				"card/DSC_1234 (photo): card/DSC_1234.JPG",
				"card/DSC_1234 (video): card/DSC_1234.MP4",
			},
		},
		{
			// The Live Photo shape. Splitting the pair is the v0.1
			// stance: the still and the clip are two captures with two
			// payloads, and nothing in a name can say "these travel
			// together" without lying about one of them.
			name:    "a live photo is two groups",
			entries: []string{"card/IMG_1234.HEIC", "card/IMG_1234.MOV"},
			want: []string{
				"card/IMG_1234 (photo): card/IMG_1234.HEIC",
				"card/IMG_1234 (video): card/IMG_1234.MOV",
			},
		},
		{
			// Same-base stills are unchanged: a RAW and its JPEG twin are
			// one capture, and the RAW masters them both.
			name:    "a raw and its jpeg twin stay one group",
			entries: []string{"card/DSC_1234.NEF", "card/DSC_1234.JPG"},
			want:    []string{"card/DSC_1234 (photo): card/DSC_1234.NEF card/DSC_1234.JPG"},
		},
		{
			// A sidecar joins the group its own name names.
			name: "sidecars follow the master their name names",
			entries: []string{
				"card/DSC_1234.NEF",
				"card/DSC_1234.MP4",
				"card/DSC_1234.NEF.xmp",
				"card/DSC_1234.MP4.xmp",
				"card/DSC_1234.xmp",
				"card/NKSC_PARAM/DSC_1234.NEF.nksc",
			},
			want: []string{
				// The bare .xmp names neither; where both kinds are
				// present it goes to the photo group.
				"card/DSC_1234 (photo): card/DSC_1234.NEF card/DSC_1234.NEF.xmp " +
					"card/DSC_1234.xmp card/NKSC_PARAM/DSC_1234.NEF.nksc",
				"card/DSC_1234 (video): card/DSC_1234.MP4 card/DSC_1234.MP4.xmp",
			},
		},
		{
			// With no photo group to join, the bare sidecar belongs to
			// the clip it sits beside.
			name:    "a bare sidecar beside a clip joins it",
			entries: []string{"card/DSC_1234.MP4", "card/DSC_1234.xmp"},
			want: []string{
				"card/DSC_1234 (video): card/DSC_1234.MP4 card/DSC_1234.xmp",
			},
		},
		{
			// An edit is a derivative of the photo it was made from, so
			// the merge stops at the boundary too.
			name:    "an edit never merges into a video group",
			entries: []string{"card/IMG.MP4", "card/IMG-Edit.tif"},
			want: []string{
				"card/IMG (video): card/IMG.MP4",
				"card/IMG-Edit (photo): card/IMG-Edit.tif",
			},
		},
		{
			name:    "an edit merges into the still beside the clip",
			entries: []string{"card/IMG.NEF", "card/IMG.MP4", "card/IMG-Edit.tif"},
			want: []string{
				"card/IMG (photo): card/IMG.NEF card/IMG-Edit.tif",
				"card/IMG (video): card/IMG.MP4",
			},
		},
		{
			// Atomicity is per group, and the still is another group:
			// selecting the clip does not drag it in.
			name:    "selecting the clip does not select the still",
			entries: []string{"card/DSC_1234.JPG", "card/DSC_1234.MP4"},
			inputs:  []string{"card/DSC_1234.MP4"},
			want:    []string{"card/DSC_1234 (video): card/DSC_1234.MP4"},
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

func TestGroupIDSeparatesKinds(t *testing.T) {
	// Two groups share the base name and so share the key; ID is what
	// tells them apart wherever groups are indexed.
	root := build(t, "card/DSC_1234.JPG", "card/DSC_1234.MP4")
	scan := collect(t, []string{root}, Options{})
	if len(scan.Groups) != 2 {
		t.Fatalf("groups = %v, want two", summary(root, scan))
	}
	first, second := scan.Groups[0], scan.Groups[1]
	if first.Key != second.Key {
		t.Errorf("keys = %q and %q, want the shared base name", first.Key, second.Key)
	}
	if first.ID() == second.ID() {
		t.Errorf("both groups have ID %q, want distinct identifiers", first.ID())
	}
	if first.Kind != KindPhoto || second.Kind != KindVideo {
		t.Errorf("kinds = %q and %q, want photo then video", first.Kind, second.Kind)
	}
}

func TestClaim(t *testing.T) {
	// Which master a name names: its own extension for a media file, the
	// appended master extension for a sidecar, nothing for a bare one.
	cases := []struct {
		path      string
		want      Kind
		wantClaim bool
	}{
		{"card/DSC_1234.NEF", KindPhoto, true},
		{"card/DSC_1234.JPG", KindPhoto, true},
		{"card/scan.tif", KindPhoto, true},
		{"card/DSC_1234.MP4", KindVideo, true},
		{"card/A001.braw", KindVideo, true},
		{"card/clip.MOV", KindVideo, true},
		{"card/DSC_1234.NEF.xmp", KindPhoto, true},
		{"card/DSC_1234.MP4.xmp", KindVideo, true},
		{"card/DSC_1234.mov.xmp", KindVideo, true},
		{"card/NKSC_PARAM/DSC_1234.NEF.nksc", KindPhoto, true},
		{"card/DSC_1234.nef.dop", KindPhoto, true},
		// No master extension in the name: no claim.
		{"card/DSC_1234.xmp", KindPhoto, false},
		{"card/DSC_1234.nksc", KindPhoto, false},
		{"card/notes.txt", KindPhoto, false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			kind, claimed := claim(filepath.FromSlash(tc.path))
			if claimed != tc.wantClaim || (claimed && kind != tc.want) {
				t.Errorf("claim(%q) = %q, %v, want %q, %v",
					tc.path, kind, claimed, tc.want, tc.wantClaim)
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
