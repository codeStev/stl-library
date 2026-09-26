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
