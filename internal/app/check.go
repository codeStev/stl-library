// Package app holds the use cases and the ports they need. It never
// touches the disk itself; adapters implement the ports.
package app

import (
	"context"

	"github.com/codeStev/stl-library/internal/core/library"
)

// Lister lists every file below the library root. It must not change
// anything on disk.
type Lister interface {
	List(ctx context.Context) ([]library.File, error)
}

// Report is the outcome of reading a library.
type Report struct {
	Files  int
	Models []*library.Model
	Issues []library.Issue
}

// Check reads the library and reports its models and the folders that
// don't follow the convention.
func Check(ctx context.Context, l Lister) (Report, error) {
	files, err := l.List(ctx)
	if err != nil {
		return Report{}, err
	}
	models, issues := library.Read(files)
	return Report{Files: len(files), Models: models, Issues: issues}, nil
}
