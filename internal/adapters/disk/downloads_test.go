package disk

import (
	"archive/zip"
	"bytes"
	"context"
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
