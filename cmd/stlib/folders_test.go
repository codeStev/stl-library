package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeparateFolders(t *testing.T) {
	root := t.TempDir()
	lib, dl := filepath.Join(root, "lib"), filepath.Join(root, "dl")
	os.MkdirAll(filepath.Join(lib, "Creator"), 0o755)
	os.MkdirAll(dl, 0o755)
	if err := separateFolders(dl, lib); err != nil {
		t.Errorf("separate folders refused: %v", err)
	}
	for _, c := range []struct{ d, l string }{
		{lib, lib}, {filepath.Join(lib, "Creator"), lib}, {root, lib}, {lib, filepath.Join(lib, "Creator")},
	} {
		if err := separateFolders(c.d, c.l); err == nil {
			t.Errorf("downloads %s and library %s accepted", c.d, c.l)
		}
	}
	// A symlink into the library is the library.
	link := filepath.Join(root, "link")
	if err := os.Symlink(lib, link); err == nil {
		if err := separateFolders(link, lib); err == nil {
			t.Error("a symlink to the library accepted")
		}
	}
}
