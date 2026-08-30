package identity

import "testing"

// FuzzParseName holds the two properties the grammar rests on: a name
// this package accepts rebuilds to itself byte for byte, and nothing
// else — however malformed — makes it panic.
func FuzzParseName(f *testing.F) {
	seeds := []string{
		"20260214_125556_1355acb2.nef",
		"20260214_125556_1355acb2.nef.xmp",
		"20240406_154315_b563e2c2-Enhanced-NR-Edit.tif",
		"20170401_185139_4aac33e0_pr.dng.pp3",
		"20260214_125556_1355acb2.tiff.xmp",
		"20260214_125556_1355acb2.NEF",
		"20260231_125556_1355acb2.nef",
		"20260214_125556_1355acb2",
		"20260214_125556_1355acb2..",
		"20230815_122948_fce9dc84(1).fp3",
		"DSC_1234.NEF",
		"....",
		"\x00",
		"",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, base string) {
		id, ok := ParseName(base)
		if !ok {
			return
		}
		if got := id.Name(); got != base {
			t.Fatalf("ParseName(%q).Name() = %q", base, got)
		}
		again, ok := ParseName(id.Name())
		if !ok || again != id {
			t.Fatalf("reparsing %q gave %+v, want %+v", id.Name(), again, id)
		}
		// A parsed name always yields the same group key its members do.
		if key := GroupKey(base); key != id.Prefix() {
			t.Fatalf("GroupKey(%q) = %q, want the prefix %q", base, key, id.Prefix())
		}
	})
}
