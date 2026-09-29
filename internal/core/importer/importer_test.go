package importer

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fs(rels ...string) []File {
	var out []File
	for _, r := range rels {
		out = append(out, File{Rel: r, Size: 1, ModUnix: 1000})
	}
	return out
}

func targets(t *testing.T, u Unit, files []File, creatorDir string) []string {
	t.Helper()
	var out []string
	for _, p := range Placements(u, files, creatorDir) {
		out = append(out, p.File.Rel+" -> "+p.Target)
	}
	sort.Strings(out)
	return out
}

func check(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestNomnomFolderLayout(t *testing.T) {
	u := Unit{Creator: "nomnom", Name: "Alduin - The Elder Scrolls V_ Skyrim"}
	got := targets(t, u, fs(
		"Presupports/1-10 Scale_Split/Lychee/wing.lys",
		"Presupports/75mm/STL/body.stl",
		"STL/1-10 Scale/alduin.stl",
		"STL/1-10 Scale_Split/wing.stl",
		"Render Images/BS_01A.jpg",
	), "nomnom")
	m := "nomnom/Alduin - The Elder Scrolls V_ Skyrim/"
	check(t, got,
		"Presupports/1-10 Scale_Split/Lychee/wing.lys -> "+m+"1-10/Supported Lychee/wing.lys",
		"Presupports/75mm/STL/body.stl -> "+m+"75mm/Supported/body.stl",
		"STL/1-10 Scale/alduin.stl -> "+m+"1-10/No Supports Combined/alduin.stl",
		"STL/1-10 Scale_Split/wing.stl -> "+m+"1-10/No Supports/wing.stl",
		"Render Images/BS_01A.jpg -> "+m+"Render Images/BS_01A.jpg",
	)
}

func TestBulkamancerZipsNamedByVariant(t *testing.T) {
	u := Unit{Creator: "bulkamancer", Name: "Musashi"}
	got := targets(t, u, fs(
		"miyamoto_musashi_no_supports/miyamoto_musashi_no_supports/arm_l.stl",
		"miyamoto_musashi_pre_supported_lys/miyamoto_musashi_pre_supported_lys/arm_l.lys",
		"miyamoto_musashi_pre_supported_stl/miyamoto_musashi_pre_supported_stl/arm_l.stl",
		"miyamoto_musashi_uncut/miyamoto_musashi_uncut/character_uncut.stl",
		"miyamoto_musashi_uncut/renders/color (1).jpg",
		"renders/color (1).jpg",
		"Readme.txt",
	), "Bulkamancer")
	m := "Bulkamancer/Musashi/"
	check(t, got,
		"miyamoto_musashi_no_supports/miyamoto_musashi_no_supports/arm_l.stl -> "+m+"No Supports/arm_l.stl",
		"miyamoto_musashi_pre_supported_lys/miyamoto_musashi_pre_supported_lys/arm_l.lys -> "+m+"Supported Lychee/arm_l.lys",
		"miyamoto_musashi_pre_supported_stl/miyamoto_musashi_pre_supported_stl/arm_l.stl -> "+m+"Supported/arm_l.stl",
		"miyamoto_musashi_uncut/miyamoto_musashi_uncut/character_uncut.stl -> "+m+"No Supports Combined/character_uncut.stl",
		"miyamoto_musashi_uncut/renders/color (1).jpg -> "+m+"renders/color (1).jpg",
		"renders/color (1).jpg -> "+m+"renders/color (1).jpg",
		"Readme.txt -> "+m+"Readme.txt",
	)
}

func TestWickedParenthesesWithAnOption(t *testing.T) {
	u := Unit{Creator: "wicked", Name: " Black Panther Portrait Bust"}
	z := func(kind string) string { return "Wicked - Black Panther Portrait Bust (" + kind + ")/" }
	got := targets(t, u, fs(
		z("Chitubox Pre Supported")+"panther.chitubox",
		z("Non Supported")+"head.stl",
		z("One Piece")+"panther.stl",
		z("Stl Pre Supported")+"head.stl",
		z("X Pose")+"hand.stl",
		z("Images")+"render.jpg",
		"Wicked - Black Panther Portrait Bust (Size and Printing Information).txt",
	), "Wicked")
	m := "Wicked/Black Panther Portrait Bust/"
	check(t, got,
		z("Chitubox Pre Supported")+"panther.chitubox -> "+m+"Supported Chitubox/panther.chitubox",
		z("Non Supported")+"head.stl -> "+m+"No Supports/head.stl",
		z("One Piece")+"panther.stl -> "+m+"No Supports Combined/panther.stl",
		z("Stl Pre Supported")+"head.stl -> "+m+"Supported/head.stl",
		z("X Pose")+"hand.stl -> "+m+"No Supports/X Pose/hand.stl",
		z("Images")+"render.jpg -> "+m+"Images/render.jpg",
		"Wicked - Black Panther Portrait Bust (Size and Printing Information).txt -> "+m+"Wicked - Black Panther Portrait Bust (Size and Printing Information).txt",
	)
}

func TestSettledNeedsQuietAndNoTemporaryFiles(t *testing.T) {
	now := time.Unix(100000, 0)
	u := Unit{Files: []File{{Rel: "a.zip", ModUnix: now.Add(-2 * time.Hour).Unix()}}}
	if ok, why := Settled(u, now, time.Hour); !ok {
		t.Errorf("quiet unit not settled: %s", why)
	}
	u.Files = append(u.Files, File{Rel: "b.zip", ModUnix: now.Add(-10 * time.Minute).Unix()})
	if ok, _ := Settled(u, now, time.Hour); ok {
		t.Error("recently changed unit settled")
	}
	u.Files = []File{{Rel: "a.zip", ModUnix: 1}, {Rel: "sub/b.zip.4c2a.partial", ModUnix: 1}}
	if ok, why := Settled(u, now, time.Hour); ok || !strings.Contains(why, "still downloading") {
		t.Errorf("partial file: %v %q", ok, why)
	}
	if ok, _ := Settled(Unit{}, now, time.Hour); ok {
		t.Error("empty unit settled")
	}
}

func TestUnitsAndContainers(t *testing.T) {
	units := Units(fs(
		"nomnom/Alduin/Presupports/75mm/a.lys",
		"nomnom/Alduin/STL/75mm/a.stl",
		"nomnom/Alduin/Render Images/r.jpg",
		"bulkamancer/Musashi/m.zip",
		"bulkamancer/Last month's models/Claire Redfield - Resident Evil/c.zip",
		"bulkamancer/Last month's models/Frieren (2026)/f.zip",
		"bulkamancer/stray.txt",
		"README.txt",
	))
	var got []string
	for _, u := range units {
		got = append(got, u.Creator+"|"+u.Path+"|"+u.Name+"|"+itoa(len(u.Files)))
	}
	check(t, got,
		"bulkamancer|bulkamancer/Last month's models/Claire Redfield - Resident Evil|Claire Redfield - Resident Evil|1",
		"bulkamancer|bulkamancer/Last month's models/Frieren (2026)|Frieren (2026)|1",
		"bulkamancer|bulkamancer/Musashi|Musashi|1",
		"nomnom|nomnom/Alduin|Alduin|3",
	)
}

func TestModelNameAndSignature(t *testing.T) {
	if got := ModelName(" Wicked - Edward Norton: Bust -20251217T040753Z-1-002", "wicked"); got != "Edward Norton_ Bust" {
		t.Errorf("model name %q", got)
	}
	a := Signature(fs("x", "y"))
	if a != Signature(fs("y", "x")) || a == Signature(fs("x")) {
		t.Error("signature")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAlternativeFoldersAreOptionsAndModelNameWordsDontBreakVariants(t *testing.T) {
	u := Unit{Creator: "bulkamancer", Name: "Frieren - Sousou no Frieren (2026)"}
	got := targets(t, u, fs(
		"frieren_no_supports/frieren_no_supports/base.stl",
		"frieren_no_supports/frieren_no_supports/alternative_split/arm.stl",
		"frieren_no_supports/renders/color (1).jpg",
		"frieren_pre_supported_lys/frieren_pre_supported_lys/base.lys",
		"frieren_uncut/frieren_uncut/character_uncut.stl",
	), "Bulkamancer")
	m := "Bulkamancer/Frieren - Sousou no Frieren (2026)/"
	check(t, got,
		"frieren_no_supports/frieren_no_supports/base.stl -> "+m+"No Supports/base.stl",
		"frieren_no_supports/frieren_no_supports/alternative_split/arm.stl -> "+m+"No Supports/alternative split/arm.stl",
		"frieren_no_supports/renders/color (1).jpg -> "+m+"renders/color (1).jpg",
		"frieren_pre_supported_lys/frieren_pre_supported_lys/base.lys -> "+m+"Supported Lychee/base.lys",
		"frieren_uncut/frieren_uncut/character_uncut.stl -> "+m+"No Supports Combined/character_uncut.stl",
	)
}

func TestTyposRepeatedExtrasAndVariantNamedFolders(t *testing.T) {
	u := Unit{Creator: "wicked", Name: "Joel Sculpture"}
	got := targets(t, u, fs(
		"Wicked - Joel Sculpture (Chitubox Pre Suppotted)/joel.chitubox",
		"Wicked - Joel Sculpture (Images)/Images/JPGS/a.jpg",
	), "Wicked")
	check(t, got,
		"Wicked - Joel Sculpture (Chitubox Pre Suppotted)/joel.chitubox -> Wicked/Joel Sculpture/Supported Chitubox/joel.chitubox",
		"Wicked - Joel Sculpture (Images)/Images/JPGS/a.jpg -> Wicked/Joel Sculpture/Images/JPGS/a.jpg",
	)
	if !NotAModel(Unit{Name: "Presupports"}) || !NotAModel(Unit{Name: "STL"}) || NotAModel(Unit{Name: "Sanji - One Piece"}) {
		t.Error("NotAModel")
	}
}

func TestHiddenTemporaryFilesAndChangeTimesKeepAFolderWaiting(t *testing.T) {
	now := time.Unix(100000, 0)
	old := now.Add(-5 * time.Hour).Unix()
	// rsync/rclone keep the original modification time; the change time
	// shows the copy just happened.
	u := Unit{Files: []File{{Rel: "a.zip", ModUnix: old, ChangeUnix: now.Add(-time.Minute).Unix()}}}
	if ok, _ := Settled(u, now, time.Hour); ok {
		t.Error("freshly copied file with an old modification time counted as settled")
	}
	// A hidden temporary file (rsync's ".name.XXXXXX") means it's still coming.
	u = Unit{Files: []File{{Rel: "a.zip", ModUnix: old, ChangeUnix: old}, {Rel: ".b.zip.Xy12Ab", Hidden: true, ModUnix: old, ChangeUnix: old}}}
	if ok, why := Settled(u, now, time.Hour); ok || !strings.Contains(why, "still downloading") {
		t.Errorf("hidden temp file: %v %q", ok, why)
	}
	// Junk doesn't block, and hidden files are never placed.
	u = Unit{Name: "M", Creator: "c", Files: []File{{Rel: "a.stl", ModUnix: old, ChangeUnix: old}, {Rel: ".DS_Store", Hidden: true, ModUnix: old}}}
	if ok, why := Settled(u, now, time.Hour); !ok {
		t.Errorf("junk blocked: %s", why)
	}
	if ps := Placements(u, u.Files, "C"); len(ps) != 1 {
		t.Errorf("hidden file placed: %+v", ps)
	}
}

func TestBustsBelongToTheirModelAsTheBustScale(t *testing.T) {
	var units []Unit
	for _, u := range Units([]File{
		{Rel: "nomnom/Busts/Kida - Atlantis/STL/k.stl"}, {Rel: "nomnom/Busts/Lune/STL/l.stl"},
		{Rel: "nomnom/July 2025 Release/Busts/Suki/Supported STL/s.stl"},
		{Rel: "nomnom/July 2025 Release/Suki/75mm/Supported STL/s.stl"},
		{Rel: "nomnom/July 2025 Release/Mewtwo/75mm/STL/m.stl"},
	}) {
		units = append(units, u)
	}
	got := map[string]bool{}
	for _, u := range units {
		got[u.Path] = u.Bust
	}
	want := map[string]bool{"nomnom/Busts/Kida - Atlantis": true, "nomnom/Busts/Lune": true,
		"nomnom/July 2025 Release/Busts/Suki": true, "nomnom/July 2025 Release/Suki": false, "nomnom/July 2025 Release/Mewtwo": false}
	if len(got) != len(want) {
		t.Fatalf("units %v", got)
	}
	for p, b := range want {
		if v, ok := got[p]; !ok || v != b {
			t.Errorf("%s: bust=%v present=%v", p, v, ok)
		}
	}
	// A bust unit's print files get the Bust scale; its renders belong to the whole model.
	u := Unit{Creator: "nomnom", Path: "nomnom/Busts/Kida - Atlantis", Name: "Kida - Atlantis", Bust: true}
	var targets []string
	for _, p := range Placements(u, []File{{Rel: "STL/k.stl"}, {Rel: "Supported Lychee/k.lys"}, {Rel: "Render Images/r.jpg"}}, "nomnom") {
		targets = append(targets, p.Target)
	}
	sort.Strings(targets)
	if strings.Join(targets, "\n") != "nomnom/Kida - Atlantis/Bust/No Supports/k.stl\nnomnom/Kida - Atlantis/Bust/Supported Lychee/k.lys\nnomnom/Kida - Atlantis/Render Images/r.jpg" {
		t.Errorf("targets:\n%s", strings.Join(targets, "\n"))
	}
	// Inside a download, a Busts folder is the Bust scale too.
	full := Unit{Creator: "nomnom", Path: "nomnom/Kida", Name: "Kida - Atlantis"}
	p := Placements(full, []File{{Rel: "STL/Busts/Supported/b.stl"}, {Rel: "STL/75mm/Supported/k.stl"}}, "nomnom")
	if p[0].Target != "nomnom/Kida - Atlantis/Bust/Supported/b.stl" {
		t.Errorf("Busts subfolder: %s", p[0].Target)
	}
}

func TestArchiveKindsAndVolumes(t *testing.T) {
	kinds := map[string]string{
		"a.zip": "zip", "A.ZIP": "zip", "a.7z": "7z", "a.rar": "rar", "a.part1.rar": "rar", "a.part01.rar": "rar",
		"a.7z.001": "7z", "a.stl": "", "a.part2.rar": "", "a.r00": "", "a.r12": "", "a.7z.002": "", "readme.txt": "", "a.tar.gz": "",
	}
	for name, want := range kinds {
		if got := ArchiveKind(name); got != want {
			t.Errorf("ArchiveKind(%q) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]bool{"a.part2.rar": true, "a.part10.rar": true, "a.part1.rar": false, "a.part01.rar": false,
		"a.r00": true, "a.7z.002": true, "a.7z.001": false, "a.rar": false, "a.zip": false} {
		if got := IsVolumePart(name); got != want {
			t.Errorf("IsVolumePart(%q) = %v, want %v", name, got, want)
		}
	}
	for in, want := range map[string]string{"d/x.part1.rar": "d/x", "d/x.rar": "d/x", "d/x.7z.001": "d/x", "d/x.7z": "d/x", "d/x.zip": "d/x", "x.part01.rar": "x"} {
		if got := ArchiveBase(in); got != want {
			t.Errorf("ArchiveBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScaleGluedToTheWordScale(t *testing.T) {
	u := Unit{Creator: "Unbekannt", Path: "Unbekannt/Persephone", Name: "God Persephone - Mythology - KingSTL.com"}
	var got []string
	for _, p := range Placements(u, fs("Persephone/1-9scale Persephone .zip/arm_left.stl", "Persephone/1-12scale Persephone .zip/arm_left.stl"), "Unbekannt") {
		got = append(got, p.Target)
	}
	sort.Strings(got)
	if len(got) != 2 || !strings.Contains(got[0], "/1-12/") || !strings.Contains(got[1], "/1-9/") {
		t.Errorf("targets: %v", got)
	}
}
