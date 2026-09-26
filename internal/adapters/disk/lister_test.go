package disk

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestListsFilesSkippingUnderscoreFoldersAndHiddenFiles(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"Loot Studios/Rel/Model/32mm/Supported/a.stl",
		"Loot Studios/Rel/Model/cover.jpg",
		"_duplicates/Loot Studios/Rel/Model/a.stl",
		"Loot Studios/.DS_Store",
		"Loot Studios/Rel/_Unsorted/x.stl",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("xx"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Lister{Root: root}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
		if f.Size != 2 || f.ModUnix == 0 {
			t.Errorf("%s: size %d mod %d", f.Path, f.Size, f.ModUnix)
		}
	}
	sort.Strings(got)
	want := "Loot Studios/Rel/Model/32mm/Supported/a.stl\nLoot Studios/Rel/Model/cover.jpg"
	if strings.Join(got, "\n") != want {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

func TestFilesOpensInsideTheRootOnly(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "C/M"), 0o755)
	os.WriteFile(filepath.Join(root, "C/M/a.stl"), []byte("abc"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(root), "secret.txt"), []byte("no"), 0o644)
	f := Files{Root: root}
	r, err := f.Open(context.Background(), "C/M/a.stl")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	for _, bad := range []string{"../secret.txt", "C/../../secret.txt", "/etc/passwd", "C//M/a.stl", "", `C\M\a.stl`} {
		if _, err := f.Open(context.Background(), bad); err != ErrOutsideRoot {
			t.Errorf("%q: err = %v, want ErrOutsideRoot", bad, err)
		}
	}
}

func TestThumbCacheRoundTrip(t *testing.T) {
	c := ThumbCache{Dir: t.TempDir()}
	if _, ok, err := c.Get("abcdef"); ok || err != nil {
		t.Fatalf("empty cache: %v %v", ok, err)
	}
	if err := c.Put("abcdef", []byte("jpg")); err != nil {
		t.Fatal(err)
	}
	if data, ok, err := c.Get("abcdef"); !ok || err != nil || string(data) != "jpg" {
		t.Errorf("got %q %v %v", data, ok, err)
	}
}

func TestThumbCacheKeysAndDelete(t *testing.T) {
	c := ThumbCache{Dir: t.TempDir()}
	k := "0123456789abcdef0123456789abcdef"
	c.Put(k, []byte("jpg"))
	os.WriteFile(filepath.Join(c.Dir, "01", "notes.txt"), []byte("x"), 0o644) // not a thumbnail
	keys, err := c.Keys()
	if err != nil || len(keys) != 1 || keys[0] != k {
		t.Fatalf("keys %v %v", keys, err)
	}
	if err := c.Delete("../../etc/passwd"); err == nil {
		t.Error("deleted a non-key")
	}
	if err := c.Delete(k); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(k); ok {
		t.Error("still cached")
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "01", "notes.txt")); err != nil {
		t.Error("removed an unrelated file")
	}
}
