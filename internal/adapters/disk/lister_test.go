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
