package identity

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// at builds a wall-clock time the way every resolution reports one.
func at(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

// The tag fixtures are real ExifTool -a -G0 output captured from files
// of each container type; only the values matter, so they are inlined.
var (
	// A Nikon NEF: DateTimeOriginal and CreateDate agree.
	nefTags = map[string]string{
		"EXIF:DateTimeOriginal": "2024:06:01 13:21:10",
		"XMP:CreateDate":        "2024:06:01 13:21:10.03",
		"EXIF:CreateDate":       "2024:06:01 13:21:10",
	}
	// An XMP sidecar: only XMP-group dates exist.
	xmpSidecarTags = map[string]string{
		"XMP:DateTimeOriginal": "2024:06:01 13:21:10.03",
		"XMP:CreateDate":       "2024:06:01 13:21:10.03",
		"XMP:DateCreated":      "2024:06:01 13:21:10.03",
	}
	// A BRAW clip: no DateTimeOriginal at all; its QuickTime CreateDate
	// is local wall-clock time (unusual for QuickTime, standard for
	// BRAW). This is why QuickTime is ranked last and not converted.
	brawTags = map[string]string{
		"QuickTime:CreateDate": "2021:08:08 14:56:53",
	}
	// A Nikon MOV: maker-notes times are local (16:09), QuickTime is UTC
	// (14:09). QuickTime sorts first among the keys — resolution must
	// not be fooled by that.
	movTags = map[string]string{
		"QuickTime:CreateDate":        "2025:06:15 14:09:10",
		"MakerNotes:DateTimeOriginal": "2025:06:15 16:09:10",
		"MakerNotes:CreateDate":       "2025:06:15 16:09:10",
	}
	// A RED R3D clip: same shape as the MOV.
	r3dTags = map[string]string{
		"MakerNotes:DateTimeOriginal": "2026:02:03 14:08:40",
		"QuickTime:CreateDate":        "2026:02:03 13:08:40",
		"MakerNotes:CreateDate":       "2026:02:03 14:08:40",
	}
)

// TestChainFixtures resolves the real-file fixtures through the chain
// their format selects. Every expectation here is the answer the Python
// line gives, tag name included.
func TestChainFixtures(t *testing.T) {
	cases := []struct {
		name   string
		ext    string
		tags   map[string]string
		want   time.Time
		source string
	}{
		{
			name: "nef resolves from EXIF DateTimeOriginal", ext: "nef", tags: nefTags,
			want: at(2024, 6, 1, 13, 21, 10), source: "EXIF:DateTimeOriginal",
		},
		{
			name: "sidecar falls through to XMP DateCreated", ext: "jpg", tags: xmpSidecarTags,
			want: at(2024, 6, 1, 13, 21, 10), source: "XMP:DateCreated",
		},
		{
			name: "braw uses the local QuickTime value", ext: "braw", tags: brawTags,
			want: at(2021, 8, 8, 14, 56, 53), source: "QuickTime:CreateDate",
		},
		{
			name: "mov prefers MakerNotes local over QuickTime utc", ext: "mov", tags: movTags,
			want: at(2025, 6, 15, 16, 9, 10), source: "MakerNotes:DateTimeOriginal",
		},
		{
			name: "r3d prefers MakerNotes", ext: "r3d", tags: r3dTags,
			want: at(2026, 2, 3, 14, 8, 40), source: "MakerNotes:DateTimeOriginal",
		},
		{
			// Without DateTimeOriginal, the unqualified CreateDate entry
			// must still skip QuickTime.
			name: "unqualified CreateDate defers QuickTime", ext: "mov",
			tags: map[string]string{
				"QuickTime:CreateDate":  "2025:06:15 14:09:10",
				"MakerNotes:CreateDate": "2025:06:15 16:09:10",
			},
			want: at(2025, 6, 15, 16, 9, 10), source: "MakerNotes:CreateDate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stamp, source, err := ResolveWith(tc.tags, ChainFor(tc.ext))
			if err != nil {
				t.Fatalf("ResolveWith: %v", err)
			}
			if !stamp.Equal(tc.want) || source != tc.source {
				t.Errorf("got %s from %s, want %s from %s",
					stamp.Format(time.RFC3339), source, tc.want.Format(time.RFC3339), tc.source)
			}

			// The format-independent chain must reach the same verdict:
			// it is the two chains concatenated, and every entry the
			// video chain would reach is one the photo chain has already
			// tried or explicitly deferred.
			stamp, source, err = Resolve(tc.tags)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !stamp.Equal(tc.want) || source != tc.source {
				t.Errorf("default chain: got %s from %s, want %s from %s",
					stamp.Format(time.RFC3339), source, tc.want.Format(time.RFC3339), tc.source)
			}
		})
	}
}

// TestRankingRules gives every precedence rule of the chain mechanism
// its own case.
func TestRankingRules(t *testing.T) {
	cases := []struct {
		name   string
		chain  []string
		tags   map[string]string
		want   time.Time
		source string
	}{
		{
			name:   "earlier entry wins over later",
			chain:  []string{"EXIF:DateTimeOriginal", "EXIF:CreateDate"},
			tags:   map[string]string{"EXIF:DateTimeOriginal": "2024:06:01 10:00:00", "EXIF:CreateDate": "2024:06:01 11:00:00"},
			want:   at(2024, 6, 1, 10, 0, 0),
			source: "EXIF:DateTimeOriginal",
		},
		{
			name:   "unusable value falls through to the next entry",
			chain:  []string{"EXIF:DateTimeOriginal", "EXIF:CreateDate"},
			tags:   map[string]string{"EXIF:DateTimeOriginal": "0000:00:00 00:00:00", "EXIF:CreateDate": "2024:06:01 11:00:00"},
			want:   at(2024, 6, 1, 11, 0, 0),
			source: "EXIF:CreateDate",
		},
		{
			name:   "unqualified entry matches any group",
			chain:  []string{"CreateDate"},
			tags:   map[string]string{"MakerNotes:CreateDate": "2024:06:01 12:00:00"},
			want:   at(2024, 6, 1, 12, 0, 0),
			source: "MakerNotes:CreateDate",
		},
		{
			name:   "unqualified entry defers a group named later in the chain",
			chain:  []string{"CreateDate", "QuickTime:CreateDate"},
			tags:   map[string]string{"QuickTime:CreateDate": "2024:06:01 10:00:00", "XMP:CreateDate": "2024:06:01 12:00:00"},
			want:   at(2024, 6, 1, 12, 0, 0),
			source: "XMP:CreateDate",
		},
		{
			name:   "a deferred group is still reached by its own entry",
			chain:  []string{"CreateDate", "QuickTime:CreateDate"},
			tags:   map[string]string{"QuickTime:CreateDate": "2024:06:01 10:00:00"},
			want:   at(2024, 6, 1, 10, 0, 0),
			source: "QuickTime:CreateDate",
		},
		{
			name:  "reservation is per tag, not per group",
			chain: []string{"EXIF:DateTimeOriginal", "CreateDate"},
			// EXIF is reserved for DateTimeOriginal only, so the
			// unqualified CreateDate may still take EXIF's.
			tags:   map[string]string{"EXIF:CreateDate": "2024:06:01 09:00:00"},
			want:   at(2024, 6, 1, 9, 0, 0),
			source: "EXIF:CreateDate",
		},
		{
			name:   "an unusable qualified value does not block a later unqualified match",
			chain:  []string{"XMP:DateTimeOriginal", "DateTimeOriginal"},
			tags:   map[string]string{"XMP:DateTimeOriginal": "2024:06:01", "MakerNotes:DateTimeOriginal": "2024:06:01 08:00:00"},
			want:   at(2024, 6, 1, 8, 0, 0),
			source: "MakerNotes:DateTimeOriginal",
		},
		{
			name:  "several eligible groups resolve in name order",
			chain: []string{"DateTimeOriginal"},
			tags: map[string]string{
				"XMP:DateTimeOriginal":        "2024:06:01 07:00:00",
				"MakerNotes:DateTimeOriginal": "2024:06:01 08:00:00",
			},
			want:   at(2024, 6, 1, 8, 0, 0),
			source: "MakerNotes:DateTimeOriginal",
		},
		{
			// Composite values are ExifTool's own derivations, so a
			// group that was actually read wins — even though
			// "Composite" sorts first.
			name:  "an unqualified entry defers the Composite group",
			chain: []string{"DateTimeOriginal"},
			tags: map[string]string{
				"Composite:DateTimeOriginal": "2024:06:01 07:00:00",
				"EXIF:DateTimeOriginal":      "2024:06:01 08:00:00",
			},
			want:   at(2024, 6, 1, 8, 0, 0),
			source: "EXIF:DateTimeOriginal",
		},
		{
			name:   "Composite still answers when it is the only candidate",
			chain:  []string{"DateTimeOriginal"},
			tags:   map[string]string{"Composite:DateTimeOriginal": "2024:06:01 07:00:00"},
			want:   at(2024, 6, 1, 7, 0, 0),
			source: "Composite:DateTimeOriginal",
		},
		{
			name:  "an explicit Composite entry matches like any qualified one",
			chain: []string{"Composite:DateTimeOriginal", "EXIF:DateTimeOriginal"},
			tags: map[string]string{
				"Composite:DateTimeOriginal": "2024:06:01 07:00:00",
				"EXIF:DateTimeOriginal":      "2024:06:01 08:00:00",
			},
			want:   at(2024, 6, 1, 7, 0, 0),
			source: "Composite:DateTimeOriginal",
		},
		{
			// The deferral is a ranking, not a filter: an unusable real
			// value still falls through to Composite.
			name:  "an unusable real value falls through to Composite",
			chain: []string{"DateTimeOriginal"},
			tags: map[string]string{
				"Composite:DateTimeOriginal": "2024:06:01 07:00:00",
				"EXIF:DateTimeOriginal":      "0000:00:00 00:00:00",
			},
			want:   at(2024, 6, 1, 7, 0, 0),
			source: "Composite:DateTimeOriginal",
		},
		{
			name:   "an unqualified value in the tags is never matched",
			chain:  []string{"DateTimeOriginal", "EXIF:CreateDate"},
			tags:   map[string]string{"DateTimeOriginal": "2024:06:01 07:00:00", "EXIF:CreateDate": "2024:06:01 08:00:00"},
			want:   at(2024, 6, 1, 8, 0, 0),
			source: "EXIF:CreateDate",
		},
		{
			name:   "a synthetic source is ranked like any other tag",
			chain:  []string{"EXIF:DateTimeOriginal", NameTimestampTag},
			tags:   map[string]string{NameTimestampTag: "2019:05:04 10:11:12"},
			want:   at(2019, 5, 4, 10, 11, 12),
			source: NameTimestampTag,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stamp, source, err := ResolveWith(tc.tags, tc.chain)
			if err != nil {
				t.Fatalf("ResolveWith: %v", err)
			}
			if !stamp.Equal(tc.want) || source != tc.source {
				t.Errorf("got %s from %s, want %s from %s",
					stamp.Format(time.RFC3339), source, tc.want.Format(time.RFC3339), tc.source)
			}
		})
	}
}

func TestQualifiedEntryIgnoresOtherGroups(t *testing.T) {
	// EXIF:DateTimeOriginal must not match the XMP DateTimeOriginal.
	if _, _, err := ResolveWith(xmpSidecarTags, []string{"EXIF:DateTimeOriginal"}); err == nil {
		t.Fatal("expected the qualified entry to ignore the XMP group")
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	// Map iteration order must never reach the answer: same input
	// state, same plan.
	first, source, err := ResolveWith(movTags, VideoChain)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	for range 50 {
		stamp, again, err := ResolveWith(movTags, VideoChain)
		if err != nil || !stamp.Equal(first) || again != source {
			t.Fatalf("unstable resolution: %v %v %v", stamp, again, err)
		}
	}
}

func TestUnresolved(t *testing.T) {
	cases := []struct {
		name     string
		tags     map[string]string
		chain    []string
		contains string
	}{
		{
			name: "no tags at all", tags: nil, chain: PhotoChain,
			contains: "no metadata tags present",
		},
		{
			name:  "unusable values are listed",
			tags:  map[string]string{"EXIF:DateTimeOriginal": "0000:00:00 00:00:00"},
			chain: []string{"EXIF:DateTimeOriginal"}, contains: "EXIF:DateTimeOriginal",
		},
		{
			// Metadata arrives with -All, so a refusal names the tags
			// the chain wanted, never the ones the file happens to have.
			name:  "unrelated tags do not resolve",
			tags:  map[string]string{"EXIF:Model": "NIKON Z 7_2"},
			chain: PhotoChain, contains: "none of EXIF:DateTimeOriginal",
		},
		{
			name: "an unusable value is quoted as the evidence",
			tags: map[string]string{
				"EXIF:DateTimeOriginal": "2024:06:01",
				"EXIF:CreateDate":       "0000:00:00 00:00:00",
			},
			chain: PhotoChain, contains: `EXIF:DateTimeOriginal="2024:06:01"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ResolveWith(tc.tags, tc.chain)
			if err == nil {
				t.Fatal("expected an unresolvable capture time")
			}
			if !errors.Is(err, ErrNoCaptureTime) {
				t.Errorf("error does not wrap ErrNoCaptureTime: %v", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %q does not name %q", err, tc.contains)
			}
		})
	}
}

// TestUnresolvedStaysReadable guards the evidence sentence against the
// -All read behind it: a file carries hundreds of tags, and a finding
// is one line.
func TestUnresolvedStaysReadable(t *testing.T) {
	tags := make(map[string]string, 500)
	for i := range 500 {
		tags["Other:Tag"+strconv.Itoa(i)] = strings.Repeat("x", 200)
	}
	tags["EXIF:DateTimeOriginal"] = strings.Repeat("9", 300)

	_, _, err := ResolveWith(tags, PhotoChain)
	if err == nil {
		t.Fatal("expected an unresolvable capture time")
	}
	if len(err.Error()) > 200 {
		t.Errorf("refusal is %d bytes long: %s", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "EXIF:DateTimeOriginal") {
		t.Errorf("refusal does not name its evidence: %s", err)
	}
}

// TestUTCMarker covers the latent conversion mechanism. No default
// chain marks an entry: no format stores UTC there reliably.
func TestUTCMarker(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("no timezone database: %v", err)
	}
	defer func(saved *time.Location) { time.Local = saved }(time.Local)
	time.Local = zone

	cases := []struct {
		name   string
		chain  []string
		tags   map[string]string
		want   time.Time
		source string
	}{
		{
			name:   "qualified entry converts and flags, summer offset",
			chain:  []string{"QuickTime:CreateDate@utc"},
			tags:   map[string]string{"QuickTime:CreateDate": "2025:06:15 14:09:10"},
			want:   at(2025, 6, 15, 16, 9, 10),
			source: "QuickTime:CreateDate@utc",
		},
		{
			name:   "unqualified marked entry names the group it took",
			chain:  []string{"CreateDate@utc"},
			tags:   map[string]string{"QuickTime:CreateDate": "2025:01:15 12:00:00"},
			want:   at(2025, 1, 15, 13, 0, 0), // winter offset
			source: "QuickTime:CreateDate@utc",
		},
		{
			name:   "unmarked entries never convert",
			chain:  []string{"QuickTime:CreateDate"},
			tags:   brawTags, // BRAW stores local time in the QuickTime atom
			want:   at(2021, 8, 8, 14, 56, 53),
			source: "QuickTime:CreateDate",
		},
		{
			name:   "the default video chain does not convert",
			chain:  VideoChain,
			tags:   map[string]string{"QuickTime:CreateDate": "2025:06:15 14:09:10"},
			want:   at(2025, 6, 15, 14, 9, 10),
			source: "QuickTime:CreateDate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stamp, source, err := ResolveWith(tc.tags, tc.chain)
			if err != nil {
				t.Fatalf("ResolveWith: %v", err)
			}
			if !stamp.Equal(tc.want) || source != tc.source {
				t.Errorf("got %s from %s, want %s from %s",
					stamp.Format(time.RFC3339), source, tc.want.Format(time.RFC3339), tc.source)
			}
		})
	}
}

func TestParseDateTime(t *testing.T) {
	cases := []struct {
		value string
		want  time.Time
		ok    bool
	}{
		{"2024:06:01 13:21:10", at(2024, 6, 1, 13, 21, 10), true},
		{"2024:06:01 13:21:10.03", at(2024, 6, 1, 13, 21, 10), true},
		{"2024:06:01 13:21:10+02:00", at(2024, 6, 1, 13, 21, 10), true},
		{"2024:06:01 13:21:10.03+02:00", at(2024, 6, 1, 13, 21, 10), true},
		{"2024:06:01 13:21:10Z", at(2024, 6, 1, 13, 21, 10), true},
		{"2024:06:01T13:21:10", at(2024, 6, 1, 13, 21, 10), true},
		{"2024-06-01T13:21:10", time.Time{}, false}, // not ExifTool's format
		{"2024:06:01", time.Time{}, false},          // date only: partial dates are poison
		{"0000:00:00 00:00:00", time.Time{}, false},
		{"2024:13:01 13:21:10", time.Time{}, false}, // impossible month
		{"2024:02:30 13:21:10", time.Time{}, false}, // impossible day
		{"2024:06:01 25:00:00", time.Time{}, false}, // impossible hour
		{" 2024:06:01 13:21:10", time.Time{}, false},
		{"", time.Time{}, false},
		{"20240601", time.Time{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			stamp, ok := ParseDateTime(tc.value)
			if ok != tc.ok || !stamp.Equal(tc.want) {
				t.Errorf("ParseDateTime(%q) = %v, %v; want %v, %v",
					tc.value, stamp, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestTimestampFromName(t *testing.T) {
	recovered := []struct {
		name string
		want time.Time
	}{
		{"20190504_101112.jpg", at(2019, 5, 4, 10, 11, 12)},
		{"VID_20190504_101112.mp4", at(2019, 5, 4, 10, 11, 12)},
		{"2016-12-31 23.59.59.png", at(2016, 12, 31, 23, 59, 59)},
		{"2016-12-31T23-59-59 party.jpg", at(2016, 12, 31, 23, 59, 59)},
		// phone cameras: compact seconds followed by milliseconds
		{"PXL_20220612_133017259.jpg", at(2022, 6, 12, 13, 30, 17)},
		{"IMG_20180707_083818835.jpg", at(2018, 7, 7, 8, 38, 18)},
		// canonical archive names carry their timestamp too
		{"20200530_125438_7ea4f4fd_ref.jpg", at(2020, 5, 30, 12, 54, 38)},
		{"2016.12.31_23.59.59.jpg", at(2016, 12, 31, 23, 59, 59)},
		{"1999-01-02 03:04:05", time.Time{}}, // colons are not an allowed separator
	}
	for _, tc := range recovered {
		t.Run(tc.name, func(t *testing.T) {
			stamp, ok := TimestampFromName(tc.name)
			if tc.want.IsZero() {
				if ok {
					t.Fatalf("TimestampFromName(%q) = %v, want none", tc.name, stamp)
				}
				return
			}
			if !ok || !stamp.Equal(tc.want) {
				t.Errorf("TimestampFromName(%q) = %v, %v; want %v",
					tc.name, stamp, ok, tc.want)
			}
		})
	}

	refused := []string{
		"31.12.2016 party.jpg",         // day-first: never interpreted
		"doc_12312016_121212.jpg",      // US month-first: never interpreted
		"IMG-20161231-WA0001.jpg",      // date only: no time to recover
		"20210329_Hania_thumbnail.jpg", // date only
		"20260101_888888.jpg",          // not a valid time
		"20261331_101112.jpg",          // not a valid month
		"20260230_101112.jpg",          // February 30th
		"2016-1231_101112.jpg",         // inconsistent date separators
		"2016-12-31 23.5959.jpg",       // inconsistent time separators
		// a real phone template bug: HH.<month>.SS in the time slot —
		// the extra text between date and time keeps it unmatched, and
		// that is correct, because its minutes field lies
		"2008.03.05 godz. 16.03.06.jpg",
		"IMG_4231.jpg",
		"18990504_101112.jpg", // before the photographic era this tool covers
		"20190504_1011129999.jpg",
		"",
	}
	for _, name := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			if stamp, ok := TimestampFromName(name); ok {
				t.Errorf("TimestampFromName(%q) = %v, want none", name, stamp)
			}
		})
	}
}

func TestChainFor(t *testing.T) {
	video := []string{"mov", ".MOV", "braw", "r3d", "mp4", "mts"}
	for _, ext := range video {
		if got := ChainFor(ext); &got[0] != &VideoChain[0] {
			t.Errorf("ChainFor(%q) is not the video chain", ext)
		}
	}
	photo := []string{"nef", ".NEF", "jpg", "dng", "tif", "xmp", ""}
	for _, ext := range photo {
		if got := ChainFor(ext); &got[0] != &PhotoChain[0] {
			t.Errorf("ChainFor(%q) is not the photo chain", ext)
		}
	}
}
