package disk

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codeStev/stl-library/internal/core/importer"
)

const sevenZipListing = `Path = Cassandra SFW Parts STLs
Folder = +
Size = 0
Attributes = D drwxr-xr-x

Path = Cassandra SFW Parts STLs/Cassandra SFW Parts STLs/cassandra_body.stl
Folder = -
Size = 15016784
Packed Size = 5000000
Modified = 2020-12-15 12:00:36.4830000
Attributes = A -rw-r--r--
CRC = 1234ABCD

Path = link.stl
Folder = -
Size = 5
Attributes = A lrwxrwxrwx
Symbolic Link = ../secret.stl
`

const unrarListing = `UNRAR 7.07 freeware      Copyright (c) 1993-2024 Alexander Roshal

Archive: Senua.part1.rar
Details: RAR 5, volume

        Name: senua_lychee_scenes/senua_base.lys
        Type: File
        Size: 235005460
 Packed size: 100
       Ratio: 42%
       mtime: 2021-09-09 21:56:00,000000000
  Attributes: -rw-r--r--
       CRC32: 12345678
     Host OS: Unix

        Name: senua_lychee_scenes
        Type: Directory
        Size: 0

        Name: elsewhere
        Type: Symbolic link
`

func TestParseListings(t *testing.T) {
	got, err := parse7zList([]byte(sevenZipListing))
	if err != nil || len(got) != 1 || got[0].Name != "Cassandra SFW Parts STLs/Cassandra SFW Parts STLs/cassandra_body.stl" ||
		got[0].Size != 15016784 || got[0].ModUnix == 0 {
		t.Errorf("7z: %+v %v", got, err)
	}
	got, err = parseUnrarList([]byte(unrarListing))
	if err != nil || len(got) != 1 || got[0].Name != "senua_lychee_scenes/senua_base.lys" || got[0].Size != 235005460 || got[0].ModUnix == 0 {
		t.Errorf("rar: %+v %v", got, err)
	}
	if _, err := parse7zList([]byte("Path = ../../etc/passwd\nFolder = -\nSize = 1\n")); err == nil {
		t.Error("an entry leaving the folder was accepted")
	}
	if _, err := parseUnrarList([]byte("        Name: /etc/passwd\n        Type: File\n        Size: 1\n")); err == nil {
		t.Error("an absolute entry was accepted")
	}
}

func TestSevenZipRoundTrip(t *testing.T) {
	sz, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("7z is not installed")
	}
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "model")
	os.MkdirAll(filepath.Join(src, "Supported"), 0o755)
	os.WriteFile(filepath.Join(src, "Supported", "head.stl"), []byte(strings.Repeat("STL", 1000)), 0o644)
	os.WriteFile(filepath.Join(src, "cover.jpg"), []byte("JPG"), 0o644)
	unit := filepath.Join(root, "wicked", "Bust")
	os.MkdirAll(unit, 0o755)
	if out, err := exec.Command(sz, "a", "-bd", filepath.Join(unit, "Bust (Pre Supported).7z"), src+string(os.PathSeparator)+".").CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(unit, "Bust (Pre Supported).part2.rar"), []byte("volume"), 0o644) // consumed with its first volume
	d := Downloads{Root: root, SevenZip: sz, TempDir: t.TempDir()}
	listed, err := d.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var files []importer.File
	for _, f := range listed {
		f.Rel = strings.TrimPrefix(f.Rel, "wicked/Bust/")
		files = append(files, f)
	}
	exp, err := d.Expand(context.Background(), "wicked/Bust", files)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range exp {
		names = append(names, f.Rel)
	}
	sort.Strings(names)
	if strings.Join(names, "|") != "Bust (Pre Supported)/Supported/head.stl|Bust (Pre Supported)/cover.jpg" {
		t.Fatalf("expanded: %v", names)
	}
	got := map[string]string{}
	err = d.Each(context.Background(), "wicked/Bust", exp, func(f importer.File, r io.Reader) error {
		b, _ := io.ReadAll(r)
		got[f.Entry] = string(b)
		return nil
	})
	if err != nil || len(got["Supported/head.stl"]) != 3000 || got["cover.jpg"] != "JPG" {
		t.Errorf("content: %v %v", err, len(got))
	}
	if entries, _ := os.ReadDir(d.TempDir); len(entries) != 0 {
		t.Errorf("temp folder not cleaned up: %d entries", len(entries))
	}
}

func TestWithoutToolsArchivesStayPlainFiles(t *testing.T) {
	d := Downloads{Root: t.TempDir()}
	in := []importer.File{{Rel: "a.7z", Size: 5}, {Rel: "b.part1.rar", Size: 5}, {Rel: "b.part2.rar", Size: 5}}
	out, err := d.Expand(context.Background(), "u", in)
	if err != nil || len(out) != 3 {
		t.Errorf("%v %v", out, err)
	}
}
