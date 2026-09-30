package disk

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/codeStev/stl-library/internal/app"
)

func TestPlateStoreSavesOnceByContentAndRefusesTooMuch(t *testing.T) {
	s := PlateStore{Dir: t.TempDir() + "/plates"}
	ctx := context.Background()
	id1, n, err := s.Save(ctx, strings.NewReader("PLATE"), 100)
	if err != nil || n != 5 || len(id1) != 64 {
		t.Fatalf("save: %q %d %v", id1, n, err)
	}
	id2, _, err := s.Save(ctx, strings.NewReader("PLATE"), 100)
	if err != nil || id2 != id1 {
		t.Errorf("the same content has the same id: %q %v", id2, err)
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 1 {
		t.Errorf("one file stored, no leftovers: %v", entries)
	}
	f, size, err := s.Open(ctx, id1)
	if err != nil || size != 5 {
		t.Fatalf("open: %d %v", size, err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "PLATE" {
		t.Errorf("content %q", data)
	}
	if _, _, err := s.Save(ctx, strings.NewReader(strings.Repeat("x", 101)), 100); !errors.Is(err, app.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, _, err := s.Save(ctx, strings.NewReader(""), 100); err == nil {
		t.Error("an empty upload is refused")
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 1 {
		t.Errorf("failed uploads leave nothing behind: %v", entries)
	}
	for _, bad := range []string{"../x", "abc", strings.Repeat("g", 64), ""} {
		if _, _, err := s.Open(ctx, bad); err == nil {
			t.Errorf("Open(%q) must fail", bad)
		}
	}
}

func TestLibraryMoveIsCarefulWithFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"A/B/Presupported", "A/B/Supported", "A/C"} {
		os.MkdirAll(root+"/"+d, 0o755)
	}
	os.WriteFile(root+"/A/B/file.txt", []byte("x"), 0o644)
	w := LibraryWriter{Root: root}
	ctx := context.Background()
	for name, c := range map[string][2]string{
		"onto a folder":   {"A/B/Presupported", "A/B/Supported"},
		"a file":          {"A/B/file.txt", "A/B/renamed.txt"},
		"same name":       {"A/B/Presupported", "A/B/Presupported"},
		"out of the root": {"A/B/Presupported", "../Presupported"},
		"missing source":  {"A/B/Nope", "A/B/Other"},
		"absolute path":   {"/etc", "/etc2"},
	} {
		if err := w.Move(ctx, c[0], c[1]); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if err := w.Move(ctx, "A/B/Presupported", "A/B/No Supports"); err != nil {
		t.Fatalf("a plain rename works: %v", err)
	}
	if _, err := os.Stat(root + "/A/B/No Supports"); err != nil {
		t.Errorf("renamed: %v", err)
	}
	if _, err := os.Stat(root + "/A/B/Supported"); err != nil {
		t.Errorf("the other folder is untouched: %v", err)
	}
}
