package disk

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codeStev/stl-library/internal/core/importer"
)

func writeZip(t *testing.T, p string, entries map[string]string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, content := range entries {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	zw.Close()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, b.Bytes(), 0o644)
}

func TestDownloadsExpandsZipsAndStreamsEntries(t *testing.T) {
	root := t.TempDir()
	unit := "bulkamancer/Musashi"
	writeZip(t, filepath.Join(root, unit, "musashi_no_supports.zip"), map[string]string{
		`musashi_no_supports\arm.stl`:  "ARM",
		`musashi_no_supports\base.stl`: "BASE",
	})
	os.WriteFile(filepath.Join(root, unit, "Readme.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(root, unit, ".hidden"), []byte("x"), 0o644)
	d := Downloads{Root: root}
	ctx := context.Background()

	listed, err := d.List(ctx)
	if err != nil || len(listed) != 3 { // the hidden file is reported, flagged
		t.Fatalf("list: %v %v", listed, err)
	}
	units := importer.Units(listed)
	files, err := d.Expand(ctx, unit, units[0].Files)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, f := range files {
		rels = append(rels, f.Rel)
	}
	sort.Strings(rels)
	if strings.Join(rels, "|") != "Readme.txt|musashi_no_supports/musashi_no_supports/arm.stl|musashi_no_supports/musashi_no_supports/base.stl" {
		t.Errorf("expanded: %v", rels)
	}
	got := map[string]string{}
	if err := d.Each(ctx, unit, files, func(f importer.File, r io.Reader) error {
		b, _ := io.ReadAll(r)
		got[f.Rel] = string(b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got["musashi_no_supports/musashi_no_supports/arm.stl"] != "ARM" || got["Readme.txt"] != "hi" {
		t.Errorf("contents: %v", got)
	}
}

func TestIncompleteAndUnsafeArchivesAreErrors(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "c/m"), 0o755)
	os.WriteFile(filepath.Join(root, "c/m/half.zip"), []byte("PK\x03\x04 truncated"), 0o644)
	writeZip(t, filepath.Join(root, "c/n/evil.zip"), map[string]string{`..\..\escape.stl`: "x"})
	d := Downloads{Root: root}
	if _, err := d.Expand(context.Background(), "c/m", []importer.File{{Rel: "half.zip"}}); err == nil {
		t.Error("truncated zip accepted")
	}
	if _, err := d.Expand(context.Background(), "c/n", []importer.File{{Rel: "evil.zip"}}); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("zip slip: %v", err)
	}
}

func TestLibraryWriterNeverOverwritesAndKeepsTheTime(t *testing.T) {
	root := t.TempDir()
	w := LibraryWriter{Root: root}
	ctx := context.Background()
	mod := time.Date(2025, 8, 15, 9, 11, 34, 0, time.UTC).Unix()
	if err := w.Write(ctx, "Wicked/Panther/No Supports/a.stl", strings.NewReader("abc"), mod); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "Wicked/Panther/No Supports/a.stl"))
	if err != nil || info.Size() != 3 || info.ModTime().Unix() != mod {
		t.Fatalf("written: %v %v", info, err)
	}
	if err := w.Write(ctx, "Wicked/Panther/No Supports/a.stl", strings.NewReader("zzz"), 0); err != ErrExists {
		t.Errorf("overwrite: %v", err)
	}
	if size, ok, _ := w.Stat(ctx, "Wicked/Panther/No Supports/a.stl"); !ok || size != 3 {
		t.Errorf("stat: %d %v", size, ok)
	}
	if _, ok, _ := w.Stat(ctx, "Wicked/Panther"); !ok {
		t.Error("folder not found")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "Wicked/Panther/No Supports"))
	if len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}
	if cs, _ := w.Creators(ctx); len(cs) != 1 || cs[0] != "Wicked" {
		t.Errorf("creators: %v", cs)
	}
	if err := w.Write(ctx, "../x.stl", strings.NewReader(""), 0); err != ErrOutsideRoot {
		t.Errorf("escape: %v", err)
	}
}

func TestDownloadsReportsHiddenTempFilesAndChangeTimes(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "wicked/Bust"), 0o755)
	os.WriteFile(filepath.Join(root, "wicked/Bust/a.zip"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "wicked/Bust/.b.zip.Xy12Ab"), []byte("x"), 0o644)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(root, "wicked/Bust/a.zip"), old, old)
	files, err := Downloads{Root: root}.List(context.Background())
	if err != nil || len(files) != 2 {
		t.Fatalf("list: %+v %v", files, err)
	}
	for _, f := range files {
		switch f.Rel {
		case "wicked/Bust/a.zip":
			if f.Hidden || f.ModUnix != old.Unix() || f.ChangeUnix < time.Now().Add(-time.Minute).Unix() {
				t.Errorf("a.zip: %+v", f)
			}
		case "wicked/Bust/.b.zip.Xy12Ab":
			if !f.Hidden {
				t.Errorf("temp file not hidden: %+v", f)
			}
		}
	}
}

func TestRemoveDeletesOnlyTheUnitsImportedFiles(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"nomnom/Kida/STL/75mm/k.stl", "nomnom/Kida/STL/75mm/base.stl", "nomnom/Kida/kida.zip",
		"nomnom/Kida/late.stl", // arrived after the import started
		"nomnom/Rem/r.stl",     // another unit of the same creator
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("x"), 0o644)
	}
	d := Downloads{Root: root}
	if err := d.Remove(context.Background(), "nomnom/Kida", []string{"STL/75mm/k.stl", "STL/75mm/base.stl", "kida.zip"}); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); return err == nil }
	if exists("nomnom/Kida/STL") || exists("nomnom/Kida/kida.zip") || !exists("nomnom/Kida/late.stl") || !exists("nomnom/Rem/r.stl") {
		t.Error("removed the wrong things")
	}
	// Once the late file is gone too, the unit folder goes - the creator folder stays.
	d.Remove(context.Background(), "nomnom/Kida", []string{"late.stl"})
	if exists("nomnom/Kida") || !exists("nomnom") {
		t.Error("unit folder left behind, or creator folder removed")
	}
	for _, bad := range []struct{ unit, rel string }{{"", "nomnom/Rem/r.stl"}, {"..", "x"}, {"nomnom/Rem", "../../outside"}} {
		if err := d.Remove(context.Background(), bad.unit, []string{bad.rel}); err == nil {
			t.Errorf("removed %q in %q", bad.rel, bad.unit)
		}
	}
	if !exists("nomnom/Rem/r.stl") {
		t.Error("a refused removal deleted something")
	}
}

func TestLibraryWriterHash(t *testing.T) {
	root := t.TempDir()
	w := LibraryWriter{Root: root}
	w.Write(context.Background(), "a/b.stl", strings.NewReader("hello"), 0)
	h, err := w.Hash(context.Background(), "a/b.stl")
	if err != nil || h != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("%s %v", h, err)
	}
}

func TestNonUTF8EntryNamesAreDecoded(t *testing.T) {
	gbk := "\xc5\xe7\xbb\xf0\xc1\xfa/original_left_leg.stl" // "喷火龙" in GBK
	if got := decodeName(gbk); got != "喷火龙/original_left_leg.stl" {
		t.Errorf("GBK: %q", got)
	}
	cp437 := "caf\x82.stl" // é in CP437, no valid GBK pair
	if got := decodeName(cp437); got != "café.stl" {
		t.Errorf("CP437: %q", got)
	}
	if got := decodeName("plain ünïcode.stl"); got != "plain ünïcode.stl" {
		t.Errorf("valid UTF-8 changed: %q", got)
	}
	// End to end: an unflagged entry name in a real zip.
	root := t.TempDir()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: gbk, Method: zip.Store})
	w.Write([]byte("STL"))
	zw.Close()
	os.MkdirAll(filepath.Join(root, "u"), 0o755)
	os.WriteFile(filepath.Join(root, "u", "x.zip"), b.Bytes(), 0o644)
	d := Downloads{Root: root}
	out, err := d.Expand(context.Background(), "u", []importer.File{{Rel: "x.zip"}})
	if err != nil || len(out) != 1 || out[0].Rel != "x/喷火龙/original_left_leg.stl" {
		t.Fatalf("%v %v", out, err)
	}
	var got string
	err = d.Each(context.Background(), "u", out, func(f importer.File, r io.Reader) error { bb, _ := io.ReadAll(r); got = string(bb); return nil })
	if err != nil || got != "STL" {
		t.Errorf("read back: %q %v", got, err)
	}
}

func TestPortableNameReplacesWhatNTFSRefuses(t *testing.T) {
	for in, want := range map[string]string{
		"Wicked/Alta�r/290726 Alta�r Image 001.jpg": "Wicked/Alta_r/290726 Alta_r Image 001.jpg",
		"a/b?c*d|e.stl":  "a/b_c_d_e.stl",
		"x/name. /f.stl": "x/name/f.stl",
		"Altaïr/ok.jpg":  "Altaïr/ok.jpg",
	} {
		if got := portableName(in); got != want {
			t.Errorf("portableName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMoveMovesAFolderToANewParentAndRefusesToReplace(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	mk("Old/R/Model/Supported/a.stl")
	mk("New/R2/Other/b.stl")
	w := LibraryWriter{Root: root}
	ctx := context.Background()
	if err := w.Move(ctx, "Old/R/Model", "New/R2/Model"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "New/R2/Model/Supported/a.stl")); err != nil {
		t.Error(err)
	}
	if err := w.Move(ctx, "New/R2/Model", "New/R2/Other"); !errors.Is(err, ErrExists) {
		t.Errorf("replace: %v", err)
	}
	if err := w.Move(ctx, "New/R2", "New/R2/Inner/R2"); err == nil {
		t.Error("moved a folder into itself")
	}
	if err := w.Move(ctx, "Old/R/Gone", "Somewhere/Gone"); err == nil {
		t.Error("moved a folder that is not there")
	}
	if err := w.Move(ctx, "../x", "Y/x"); err == nil {
		t.Error("left the root")
	}
	if err := w.Move(ctx, "New/R2/Model/Supported/a.stl", "New/a.stl"); err == nil {
		t.Error("moved a file")
	}
}
