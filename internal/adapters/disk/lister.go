// Package disk reads a library from the file system. It only reads.
package disk

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/codeStev/stl-library/internal/core/library"
)

// Lister walks a library root. Folders whose name starts with "_" (like
// "_duplicates") and hidden files are skipped: they are not part of the
// library.
type Lister struct {
	Root string
}

func (l Lister) List(ctx context.Context) ([]library.File, error) {
	var out []library.File
	err := filepath.WalkDir(l.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := d.Name()
		if p != l.Root && (strings.HasPrefix(name, "_") && d.IsDir() || strings.HasPrefix(name, ".")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.Root, p)
		if err != nil {
			return err
		}
		out = append(out, library.File{Path: filepath.ToSlash(rel), Size: info.Size(), ModUnix: info.ModTime().Unix()})
		return nil
	})
	return out, err
}
