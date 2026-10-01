package app

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Linker replaces a library file by a hard link to an identical one (after
// checking the content again). It returns the file's modification time
// afterwards.
type Linker interface {
	Link(ctx context.Context, keep, other string) (modUnix int64, err error)
	// Remove deletes the file other after checking that it has the content of keep.
	Remove(ctx context.Context, keep, other string) error
}

// MergeStore is what merging duplicates needs from the index.
type MergeStore interface {
	DuplicateGroup(ctx context.Context, sha string, size int64) (DupGroup, error)
	SaveMerge(ctx context.Context, path string, size, modUnix, atUnix int64) error
}

// MergeDuplicates keeps one copy of a duplicate group (the part keepPart) and makes every
// other copy a hard link to it. The files stay where they are, so every model keeps its
// complete set of files; only the space is shared. With remove, the other copies are deleted
// instead (the models then lack those files). It returns how many files were merged or removed.
func MergeDuplicates(ctx context.Context, s MergeStore, l Linker, sha string, size, keepPart int64, remove bool, now time.Time) (int, error) {
	g, err := s.DuplicateGroup(ctx, sha, size)
	if err != nil {
		return 0, err
	}
	var keep string
	for _, f := range g.Files {
		if f.PartID == keepPart {
			keep = f.Path
		}
	}
	if keep == "" || len(g.Files) < 2 {
		return 0, fmt.Errorf("%w: the file to keep is not one of the copies", ErrInvalid)
	}
	merged := 0
	var errs []error
	for _, f := range g.Files {
		if f.PartID == keepPart || f.Linked && !remove {
			continue
		}
		if remove {
			if err := l.Remove(ctx, keep, f.Path); err != nil {
				errs = append(errs, err)
				continue
			}
			merged++
			continue
		}
		mod, err := l.Link(ctx, keep, f.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.SaveMerge(ctx, f.Path, size, mod, now.Unix()); err != nil {
			return merged, err
		}
		merged++
	}
	return merged, errors.Join(errs...)
}
