package app

import "testing"

func TestFindSetGapsComparesVariantsThatDifferOnlyInSupports(t *testing.T) {
	vs := []SetVariant{
		{ModelID: 1, ModelName: "Orc", Key: "32mm|", Supports: "Supported", Label: "32mm Supported", Files: []string{"a/Head_Supported.stl", "a/Body_Supported.stl", "a/Axe_Supported.stl"}},
		{ModelID: 1, ModelName: "Orc", Key: "32mm|", Supports: "No Supports", Label: "32mm No Supports", Files: []string{"b/Head.stl", "b/Body.stl"}},
		{ModelID: 2, ModelName: "Elf", Key: "32mm|", Supports: "Supported", Files: []string{"x.stl", "y.stl"}},
		{ModelID: 2, ModelName: "Elf", Key: "32mm|", Supports: "No Supports", Files: []string{"x.stl", "z.stl"}},
		{ModelID: 3, ModelName: "Two scales", Key: "32mm|", Supports: "Supported", Files: []string{"a.stl"}},
		{ModelID: 3, ModelName: "Two scales", Key: "75mm|", Supports: "Supported", Files: []string{"a.stl", "b.stl"}},
	}
	gaps := FindSetGaps(vs)
	if len(gaps) != 1 || gaps[0].ModelName != "Orc" {
		t.Fatalf("gaps %+v", gaps)
	}
	g := gaps[0]
	if g.Sides[0].Files != 3 || g.Sides[1].Files != 2 || len(g.Missing) != 1 || g.Missing[0] != "Axe_Supported.stl" {
		t.Errorf("gap %+v", g)
	}
}

func TestPartKeyIgnoresSupportWords(t *testing.T) {
	for _, n := range []string{"Head.stl", "Head_Supported.stl", "head-presupported.STL", "Head (No Supports).stl", "Head_sup.stl"} {
		if k := partKey(n); k != "head" {
			t.Errorf("%q -> %q", n, k)
		}
	}
}
