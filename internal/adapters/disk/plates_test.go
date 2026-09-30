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
