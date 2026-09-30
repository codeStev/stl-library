package disk

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codeStev/stl-library/internal/app"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func kinds(items []app.TidyItem) string {
	var s []string
	for _, i := range items {
		s = append(s, i.Kind+" "+i.Path)
	}
	sort.Strings(s)
	return strings.Join(s, "\n")
}

func TestTidierFindsJunkEmptyFoldersAndIdenticalCopies(t *testing.T) {
	root := t.TempDir()
	put(t, root, "C/M/Supported/Head.stl", "same")
	put(t, root, "C/M/Supported/Head (imported).stl", "same")
	put(t, root, "C/M/Supported/Arm.stl", "one")
	put(t, root, "C/M/Supported/Arm (2).stl", "two") // differs: stays
	put(t, root, "C/M/.DS_Store", "x")
	put(t, root, "C/M/__MACOSX/M/._Head.stl", "x")
	put(t, root, "C/Empty/Sub/.DS_Store", "x")
	put(t, root, "C/Keep/__MACOSX/real.stl", "real") // holds more than junk: untouched
	put(t, root, "_duplicates/C/old (2).stl", "a")
	put(t, root, "_duplicates/C/old.stl", "a")
	items, err := LibraryTidier{Root: root}.Find(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `copy C/M/Supported/Head (imported).stl
empty-folder C/Empty
empty-folder C/Empty/Sub
junk-file C/Empty/Sub/.DS_Store
junk-file C/M/.DS_Store
junk-folder C/M/__MACOSX`
	if got := kinds(items); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTidierRemovesOnlyWhatItStillVerifies(t *testing.T) {
	root := t.TempDir()
	put(t, root, "C/M/Head.stl", "same")
	put(t, root, "C/M/Head (imported).stl", "same")
	put(t, root, "C/E/.DS_Store", "x")
	tid := LibraryTidier{Root: root}
	ctx := context.Background()
	t.Run("copy moves to _duplicates", func(t *testing.T) {
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyCopy, Path: "C/M/Head (imported).stl", Original: "C/M/Head.stl"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "_duplicates/C/M/Head (imported).stl")); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(filepath.Join(root, "C/M/Head.stl")); err != nil {
			t.Error("original must stay")
		}
	})
	t.Run("a copy that changed is refused", func(t *testing.T) {
		put(t, root, "C/M/Head (2).stl", "different")
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyCopy, Path: "C/M/Head (2).stl", Original: "C/M/Head.stl"}); err == nil {
			t.Error("removed a file with other content")
		}
	})
	t.Run("a folder with a model file is refused", func(t *testing.T) {
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyEmptyFolder, Path: "C/M"}); err == nil {
			t.Error("removed a folder with files")
		}
		if _, err := os.Stat(filepath.Join(root, "C/M/Head.stl")); err != nil {
			t.Error(err)
		}
	})
	t.Run("a folder with only junk goes", func(t *testing.T) {
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyEmptyFolder, Path: "C/E"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "C/E")); !os.IsNotExist(err) {
			t.Error("still there")
		}
	})
	t.Run("a real file is not junk", func(t *testing.T) {
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyJunkFile, Path: "C/M/Head.stl"}); err == nil {
			t.Error("deleted a model file")
		}
	})
	t.Run("paths outside the library are refused", func(t *testing.T) {
		if err := tid.Remove(ctx, app.TidyItem{Kind: app.TidyJunkFile, Path: "../x/.DS_Store"}); err == nil {
			t.Error("left the root")
		}
	})
}
