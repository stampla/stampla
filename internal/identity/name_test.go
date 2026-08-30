package identity

import (
	"testing"
	"time"
)

func TestParseNameCanonical(t *testing.T) {
	cases := []struct {
		filename string
		suffix   string
		rawExt   string
		ext      string
	}{
		// Master RAW and its DAM sidecar
		{"20260214_125556_1355acb2.nef", "", "", "nef"},
		{"20260214_125556_1355acb2.xmp", "", "", "xmp"},
		// Append-style sidecars keeping the master's extension
		{"20260214_125556_1355acb2.nef.xmp", "", "nef", "xmp"},
		{"20220523_192742_d3147a94.nef.nksc", "", "nef", "nksc"},
		{"20170401_185236_c9e80f84.rw2.pp3", "", "rw2", "pp3"},
		// tif and tiff must not be confused for one another
		{"20260214_125556_1355acb2.tiff.xmp", "", "tiff", "xmp"},
		{"20260214_125556_1355acb2.tiff", "", "", "tiff"},
		{"20260214_125556_1355acb2.tif.xmp", "", "tif", "xmp"},
		// Editor derivatives with a label suffix
		{"20220401_220820_03afe50f-Edit.tif", "-Edit", "", "tif"},
		{"20240406_154315_b563e2c2-Enhanced-NR-Edit.tif", "-Enhanced-NR-Edit", "", "tif"},
		{"20221231_192158_abbc654f-Enhanced-NR.dng", "-Enhanced-NR", "", "dng"},
		// Sidecar of a suffixed derivative
		{"20170401_185139_4aac33e0_pr.dng.pp3", "_pr", "dng", "pp3"},
		{"20250221_171634_b8ea3318-Enhanced-NR.dng.xmp", "-Enhanced-NR", "dng", "xmp"},
		{"20250329_132258_20d3d649_dxo-Enhanced-SR.dng.xmp", "_dxo-Enhanced-SR", "dng", "xmp"},
		// Video
		{"20210808_145653_941930e9.braw", "", "", "braw"},
		{"20260203_140840_c4d2affb.r3d", "", "", "r3d"},
		// Leap day: a real date the parser must not refuse
		{"20240229_120000_deadbeef.nef", "", "", "nef"},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			id, ok := ParseName(tc.filename)
			if !ok {
				t.Fatalf("ParseName(%q) refused a canonical name", tc.filename)
			}
			if id.Suffix != tc.suffix || id.RawExt != tc.rawExt || id.Ext != tc.ext {
				t.Errorf("got suffix %q, rawext %q, ext %q; want %q, %q, %q",
					id.Suffix, id.RawExt, id.Ext, tc.suffix, tc.rawExt, tc.ext)
			}
			if id.Name() != tc.filename {
				t.Errorf("Name() = %q, want %q (round trip)", id.Name(), tc.filename)
			}
		})
	}
}

func TestParseNameParts(t *testing.T) {
	id, ok := ParseName("20260214_125556_1355acb2.nef.xmp")
	if !ok {
		t.Fatal("ParseName refused a canonical name")
	}
	if want := at(2026, 2, 14, 12, 55, 56); !id.Time.Equal(want) {
		t.Errorf("Time = %v, want %v", id.Time, want)
	}
	if id.Hash != "1355acb2" {
		t.Errorf("Hash = %q, want 1355acb2", id.Hash)
	}
	if id.Prefix() != "20260214_125556_1355acb2" {
		t.Errorf("Prefix() = %q", id.Prefix())
	}
	if id.IsMaster() {
		t.Error("a sidecar must not read as its group's master")
	}
}

func TestParseNameRefused(t *testing.T) {
	cases := []string{
		"20230815_122948_fce9dc84(1).fp3",      // stray copy marker before the extension
		"20230815_122948_fce9dc84(1).FP3",      // same, with an uppercase extension
		"20180424_131902_4c497be4.rw2.8se.spd", // two chained raw extensions
		"20260214_125556_1355acb2.NEF",         // uppercase extension is not canonical
		"20260214_125556_1355acb2.Nef",
		"20260214_125556_1355ACB2.nef", // uppercase hash slice is not canonical
		"20260214_125556_1355acb2",     // no extension
		"20260214_125556_1355acb2.",    // empty extension
		"20260214_125556_1355acb2..nef",
		"20260214_125556_1355acb2.nef.xmp.xmp", // only one appended master extension
		"20260214_125556_1355acb2.jpg.xmp",     // jpg is not a raw extension to append
		"20260214_125556_1355acb2beef.nef",     // a longer hash slice is another scheme
		"20260214_125556_1355acb.nef",          // a shorter one is nobody's
		"20260214_125556_1355acg2.nef",         // g is not hexadecimal
		"20260231_125556_1355acb2.nef",         // February 31st
		"20261301_125556_1355acb2.nef",         // month 13
		"20260214_256556_1355acb2.nef",         // hour 25
		"md5.txt",                              // ordinary files stay ordinary
		"generate_md5.sh",
		"DSC_1234.NEF",
		"IMG_5678.jpg",
		"20130310-20130310_172613_3577e7ff.dng", // junk prepended before the prefix
		"x20260214_125556_1355acb2.nef",
		"",
	}

	for _, filename := range cases {
		t.Run(filename, func(t *testing.T) {
			if id, ok := ParseName(filename); ok {
				t.Errorf("ParseName(%q) accepted it as %+v", filename, id)
			}
		})
	}
}

func TestPrefixSwapPreservesEverythingElse(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"20260214_125556_1355acb2.nef", "20300101_000000_deadbeef.nef"},
		{"20260214_125556_1355acb2.nef.xmp", "20300101_000000_deadbeef.nef.xmp"},
		{
			"20240406_154315_b563e2c2-Enhanced-NR-Edit.tif",
			"20300101_000000_deadbeef-Enhanced-NR-Edit.tif",
		},
		{"20170401_185139_4aac33e0_pr.dng.pp3", "20300101_000000_deadbeef_pr.dng.pp3"},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			id, ok := ParseName(tc.filename)
			if !ok {
				t.Fatalf("ParseName(%q) refused a canonical name", tc.filename)
			}
			id.Time = at(2030, 1, 1, 0, 0, 0)
			id.Hash = "deadbeef"
			if got := id.Name(); got != tc.want {
				t.Errorf("Name() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsMaster(t *testing.T) {
	cases := []struct {
		filename string
		want     bool
	}{
		{"20220523_192742_d3147a94.nef", true},
		{"20210808_145653_941930e9.braw", true},
		{"20220523_192742_d3147a94.jpg", true},
		{"20220523_192742_d3147a94.xmp", false},      // a sidecar carries no hash
		{"20220523_192742_d3147a94.nef.xmp", false},  // nor an appended one
		{"20220523_192742_d3147a94-Edit.tif", false}, // a derivative is not the master
		{"20220523_192742_d3147a94.nef.nksc", false},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			id, ok := ParseName(tc.filename)
			if !ok {
				t.Fatalf("ParseName(%q) refused a canonical name", tc.filename)
			}
			if got := id.IsMaster(); got != tc.want {
				t.Errorf("IsMaster() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNameRoundTrip(t *testing.T) {
	identities := []Identity{
		{Time: at(2026, 2, 14, 12, 55, 56), Hash: "1355acb2", Ext: "nef"},
		{Time: at(1970, 1, 1, 0, 0, 0), Hash: "00000000", Ext: "jpg"},
		{Time: at(2099, 12, 31, 23, 59, 59), Hash: "ffffffff", Ext: "r3d"},
		{Time: at(2026, 2, 14, 12, 55, 56), Hash: "1355acb2", Ext: "xmp", RawExt: "nef"},
		{Time: at(2026, 2, 14, 12, 55, 56), Hash: "1355acb2", Ext: "tif", Suffix: "-Edit"},
		{
			Time: at(2026, 2, 14, 12, 55, 56), Hash: "1355acb2",
			Ext: "pp3", Suffix: "_pr", RawExt: "dng",
		},
	}

	for _, want := range identities {
		t.Run(want.Name(), func(t *testing.T) {
			got, ok := ParseName(want.Name())
			if !ok {
				t.Fatalf("ParseName(%q) refused a name this package built", want.Name())
			}
			if !got.Time.Equal(want.Time) || got.Hash != want.Hash || got.Ext != want.Ext ||
				got.Suffix != want.Suffix || got.RawExt != want.RawExt {
				t.Errorf("round trip changed %+v into %+v", want, got)
			}
			if got.Name() != want.Name() {
				t.Errorf("Name() = %q, want %q", got.Name(), want.Name())
			}
		})
	}
}

func TestNameUsesWallClockFields(t *testing.T) {
	// A time carrying a zone must still name the wall-clock reading a
	// person at the scene would have seen.
	zone := time.FixedZone("+07:00", 7*3600)
	id := Identity{
		Time: time.Date(2026, 2, 14, 12, 55, 56, 0, zone),
		Hash: "1355acb2", Ext: "nef",
	}
	if got := id.Name(); got != "20260214_125556_1355acb2.nef" {
		t.Errorf("Name() = %q", got)
	}
}
