package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// separateFolders refuses a downloads folder that is the library, inside
// it, or contains it: an import that deletes what it copied must never be
// able to delete the library's own files.
func separateFolders(downloads, library string) error {
	d, err := realPath(downloads)
	if err != nil {
		return err
	}
	l, err := realPath(library)
	if err != nil {
		return err
	}
	if within(d, l) || within(l, d) {
		return fmt.Errorf("the downloads folder (%s) and the library (%s) must be separate folders, neither inside the other", d, l)
	}
	return nil
}

func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r, nil
	}
	return abs, nil // not there (yet)
}

// within reports whether a is b or below it.
func within(a, b string) bool {
	rel, err := filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
