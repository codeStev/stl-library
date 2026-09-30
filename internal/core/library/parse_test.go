package library

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func files(paths ...string) []File {
	var fs []File
	for _, p := range paths {
		fs = append(fs, File{Path: p, Size: 1})
	}
	return fs
}

// describe renders models compactly: "Creator|Release|Category|Model: dims[/option] (n parts)".
func describe(models []*Model) []string {
	var out []string
	for _, m := range models {
		var vs []string
		for _, v := range m.Variants {
			d := strings.Join(nonEmpty(v.Dims.Scale, v.Dims.Supports, v.Dims.Density, v.Dims.Format, v.Dims.Split, v.Dims.Extra, v.Dims.Fill, v.Dims.Tech), " ")
			if v.Option != "" {
				d += "/" + v.Option
			}
			vs = append(vs, fmt.Sprintf("[%s]x%d", d, len(v.Parts)))
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s: %s img=%d", m.Creator, m.Release, m.Category, m.Name, strings.Join(vs, " "), len(m.Images)))
	}
	return out
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func check(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestReleaseCategoryModelWithNestedVariants(t *testing.T) {
	m := "Loot Studios/Abyssal Haze/Enemies/Bell Head"
	models, issues := Read(files(
		m+"/32mm/Supported Lychee/bell.lys",
		m+"/32mm/Supported Lychee/base.lys",
		m+"/32mm/No Supports/bell.stl",
		m+"/75mm/Supported/Hollow/bell.stl",
		m+"/BellHead.jpg",
		m+"/32mm/parts.jpg",
	))
	check(t, describe(models), []string{
		"Loot Studios|Abyssal Haze|Enemies|Bell Head: [32mm No Supports]x1 [32mm Supported Lychee]x2 [75mm Supported Hollow]x1 img=2",
	})
	if len(issues) != 0 {
		t.Errorf("issues: %v", issues)
	}
}

func TestModelDirectlyUnderCreatorAndUnderCreatorCategory(t *testing.T) {
	models, _ := Read(files(
		"Fleshcraft Studio/Yiggral/Supported Lychee/y.lys",
		"Fleshcraft Studio/Yiggral/Supported Lychee/Base/b.lys",
		"Fleshcraft Studio/Busts/Kain/Bust/No Supports 3MF/FDM/stand.3mf",
		"GsArgent/Paint Rack/rack.stl",
	))
	check(t, describe(models), []string{
		"Fleshcraft Studio|||Yiggral: [Supported Lychee]x1 [Supported Lychee/Base]x1 img=0",
		"Fleshcraft Studio||Busts|Kain: [Bust No Supports 3MF FDM]x1 img=0",
		"GsArgent|||Paint Rack: []x1 img=0",
	})
}

func TestOptionsWithoutVariantLevelsByDepth(t *testing.T) {
	rel := "Archvillain Games/Khazaad Steelbreaker Clan"
	models, _ := Read(files(
		rel+"/Stonewurm Riders/Pose01/a.stl",
		rel+"/Stonewurm Riders/Pose02/a.stl",
		rel+"/King Thrag Steelhammer/k.stl",
	))
	check(t, describe(models), []string{
		"Archvillain Games|Khazaad Steelbreaker Clan||King Thrag Steelhammer: []x1 img=0",
		"Archvillain Games|Khazaad Steelbreaker Clan||Stonewurm Riders: [/Pose01]x1 [/Pose02]x1 img=0",
	})
}

func TestCombinedAndOptionInsideVariant(t *testing.T) {
	m := "nomnom/August 2025 Release/Balrog - Lord of the Rings"
	models, _ := Read(files(
		m+"/178mm/STL Combined/whole.stl",
		m+"/178mm/STL/arm.stl",
		m+"/32mm/Supported/Helmet Version/h.stl",
	))
	check(t, describe(models), []string{
		"nomnom|August 2025 Release||Balrog - Lord of the Rings: [178mm STL]x1 [178mm STL Combined]x1 [32mm Supported/Helmet Version]x1 img=0",
	})
}

func TestNonConformingFoldersAreIssuesAndLeftOut(t *testing.T) {
	models, issues := Read(files(
		"Lord of the Print/Unchained/Lord Of the Print_Unchained_December 2022/Araki/Presupported/a.stl",
		"Loot Studios/Rel/Model/Supported/32mm/x.stl",
		"Unbekannt/ana 35mm.stl",
		"loose.stl",
		"A/B/C/D/E/F/deep.stl",
		"Loot Studios/Rel/Good/32mm/Supported/g.stl",
	))
	check(t, describe(models), []string{"Loot Studios|Rel||Good: [32mm Supported]x1 img=0"})
	var got []string
	for _, i := range issues {
		got = append(got, i.Dir+": "+i.Reason)
	}
	check(t, got, []string{
		": print files directly in the library root",
		"A/B/C/D/E/F: too many folder levels above the model (expected <Creator>/<Release>/[<Category>]/<Model>)",
		`Lord of the Print/Unchained/Lord Of the Print_Unchained_December 2022/Araki/Presupported: folder "Presupported" looks like a variant level but isn't spelled canonically`,
		"Loot Studios/Rel/Model/Supported/32mm: variant levels out of order or repeated (expected scale, supports, fill, tech)",
		"Unbekannt: print files directly in a creator folder",
	})
}

func TestImagesOutsideModelsAreIgnored(t *testing.T) {
	models, issues := Read(files("Loot Studios/cover.jpg", "Loot Studios/Rel/Model/m.stl"))
	if len(models) != 1 || len(models[0].Images) != 0 || len(issues) != 0 {
		t.Errorf("models=%v issues=%v", describe(models), issues)
	}
}

func TestSignatureChangesWithContentOnly(t *testing.T) {
	read := func(fs ...File) string {
		ms, _ := Read(fs)
		return ms[0].Signature()
	}
	a := File{Path: "C/R/M/32mm/Supported/a.stl", Size: 10, ModUnix: 1}
	img := File{Path: "C/R/M/cover.jpg", Size: 5, ModUnix: 1}
	b := File{Path: "C/R/M/32mm/Supported/b.stl", Size: 3, ModUnix: 1}
	base := read(a, b, img)
	if read(img, b, a) != base {
		t.Error("order of the listing changed the signature")
	}
	a2 := a
	a2.Size = 11
	if read(a2, b, img) == base {
		t.Error("a changed part size kept the signature")
	}
	img2 := img
	img2.ModUnix = 2
	if read(a, b, img2) == base {
		t.Error("a changed image kept the signature")
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"  Painted ", "painted", "", "to   print", strings.Repeat("x", 41), "Dragon"})
	want := []string{"Painted", "to print", "Dragon"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	many := make([]string, 50)
	for i := range many {
		many[i] = fmt.Sprint("t", i)
	}
	if len(NormalizeTags(many)) != MaxTags {
		t.Error("tag limit not applied")
	}
}

func TestSolidAndHollowInsideAModelNameAreWordsOfIt(t *testing.T) {
	models, issues := Read(files(
		"Bulkamancer/EVA - Metal Gear Solid Delta Snake Eater/Supported/a.stl",
		"Bulkamancer/Hollow Knight/Supported/b.stl",
		"Bulkamancer/Solid Snake/Supported/c.stl",
		"Bulkamancer/Dragon_Solid/Supported/d.stl",
		"Bulkamancer/Mask Hollow/Supported/e.stl",
	))
	var names []string
	for _, m := range models {
		names = append(names, m.Name)
	}
	check(t, names, []string{"EVA - Metal Gear Solid Delta Snake Eater", "Hollow Knight", "Solid Snake"})
	var bad []string
	for _, i := range issues {
		bad = append(bad, i.Dir)
	}
	check(t, bad, []string{"Bulkamancer/Dragon_Solid/Supported", "Bulkamancer/Mask Hollow/Supported"})
}

func TestSuggestedSpellingOfVariantFolders(t *testing.T) {
	for name, want := range map[string]string{
		"Presupported":         "Supported",
		"pre_supported_stl":    "Supported STL",
		"PreSupported":         "Supported",
		"Unsupported":          "No Supports",
		"Non-Supported Files":  "No Supports",
		"No_Supports_Lychee":   "No Supports Lychee",
		"LYS":                  "Lychee",
		"hollow":               "Hollow",
		"STL files":            "STL",
		"Pre-Supported Lychee": "Supported Lychee",
		// a name that says several things is split into the canonical levels
		"32mm_Supported":       "32mm/Supported",
		"Supported 75mm Solid": "75mm/Supported/Solid",
		// not suggested: already canonical, names with other words, conflicting words
		"Supported":             "",
		"No Supports":           "",
		"Dragon Solid":          "",
		"Solid Snake":           "",
		"Hollow Knight":         "",
		"Supported Unsupported": "",
		"lys stl":               "",
		"":                      "",
	} {
		segs, ok := suggestSegment(name)
		got := strings.Join(segs, "/")
		if got != want || ok != (want != "") {
			t.Errorf("suggestSegment(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestSuggestRenamesOneLevelAndCountsTheIssuesItClears(t *testing.T) {
	fixes := Suggest([]string{
		"Creator/Rel/Model/Unsupported",
		"Creator/Rel/Model/Unsupported/Head",
		"Creator/Rel/Other/Presupported",
		"Creator/Rel/Dragon Solid/Supported",
		"Creator/Rel/Third/Supported_32mm",
		"Unsupported", // a creator-level folder is never renamed
	})
	var got []string
	for _, f := range fixes {
		got = append(got, f.From+" -> "+f.To+" x"+strconvI(f.Issues))
	}
	check(t, got, []string{
		"Creator/Rel/Model/Unsupported -> Creator/Rel/Model/No Supports x2",
		"Creator/Rel/Other/Presupported -> Creator/Rel/Other/Supported x1",
		"Creator/Rel/Third/Supported_32mm -> Creator/Rel/Third/32mm/Supported x1",
	})
}

func strconvI(n int) string { return string(rune('0' + n)) }
