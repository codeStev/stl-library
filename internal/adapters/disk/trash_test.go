package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrashKeepsRestoresAndPurgesDeletedFiles(t *testing.T) {
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
	now := time.Unix(1_000_000, 0)
	l := LibraryLinker{Root: root, Trash: true, Now: func() time.Time { return now }}
	ctx := context.Background()
	if err := l.Remove(ctx, "A/a.stl", "B/b.stl"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "B/b.stl")); !os.IsNotExist(err) {
		t.Fatal("file still in the library")
	}
	tr := LibraryTrash{Root: root}
	items, err := tr.List(ctx)
	if err != nil || len(items) != 1 || items[0].Path != "B/b.stl" || items[0].DeletedUnix != now.Unix() || items[0].Size != 4 {
		t.Fatalf("list %+v %v", items, err)
	}
	// Restoring puts it back; a taken place is refused.
	if p, err := tr.Restore(ctx, items[0].ID); err != nil || p != "B/b.stl" {
		t.Fatalf("restore %q %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "B/b.stl")); string(b) != "same" {
		t.Errorf("restored content %q", b)
	}
	if items, _ := tr.List(ctx); len(items) != 0 {
		t.Errorf("trash not empty: %+v", items)
	}
	// Delete again, then purge by age.
	if err := l.Remove(ctx, "A/a.stl", "B/b.stl"); err != nil {
		t.Fatal(err)
	}
	if n, _ := tr.Purge(ctx, now.Add(-time.Hour)); n != 0 {
		t.Errorf("purged a recent deletion: %d", n)
	}
	if n, err := tr.Purge(ctx, now.Add(time.Hour)); n != 1 || err != nil {
		t.Errorf("purge: %d %v", n, err)
	}
	if _, err := tr.Restore(ctx, "../etc/passwd"); err == nil {
		t.Error("odd id accepted")
	}
	if _, err := tr.Restore(ctx, "123/../../x"); err == nil {
		t.Error("escaping id accepted")
	}
}
