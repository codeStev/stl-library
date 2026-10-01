package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLinkMergesIdenticalFilesAndRefusesDifferentOnes(t *testing.T) {
	root := t.TempDir()
	write := func(rel, c string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A/a.stl", "same")
	write("B/b.stl", "same")
	write("B/c.stl", "diff")
	l := LibraryLinker{Root: root}
	ctx := context.Background()
	if _, err := l.Link(ctx, "A/a.stl", "B/c.stl"); err == nil {
		t.Fatal("linked different content")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "B/c.stl")); string(b) != "diff" {
		t.Errorf("a refused link changed the file: %q", b)
	}
	if _, err := l.Link(ctx, "A/a.stl", "B/b.stl"); err != nil {
		t.Fatal(err)
	}
	ai, _ := os.Stat(filepath.Join(root, "A/a.stl"))
	bi, _ := os.Stat(filepath.Join(root, "B/b.stl"))
	if !os.SameFile(ai, bi) {
		t.Error("not linked")
	}
	if _, err := l.Link(ctx, "A/a.stl", "B/b.stl"); err != nil {
		t.Errorf("linking again: %v", err)
	}
	write("C/c.stl", "same")
	if err := l.Remove(ctx, "A/a.stl", "B/c.stl"); err == nil {
		t.Error("removed a file with other content")
	}
	if err := l.Remove(ctx, "A/a.stl", "A/a.stl"); err == nil {
		t.Error("removed the kept file")
	}
	if err := l.Remove(ctx, "A/a.stl", "C/c.stl"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "C/c.stl")); !os.IsNotExist(err) {
		t.Error("copy still there")
	}
	if _, err := l.Link(ctx, "../x", "B/b.stl"); err == nil {
		t.Error("path outside the root accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "B")); len(entries) != 2 {
		t.Errorf("temporary files left: %v", entries)
	}
}
