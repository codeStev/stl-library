package app

import (
	"context"
	"time"
)

// TrashItem is a library file that was deleted on purpose and can still be brought back.
type TrashItem struct {
	ID          string // opaque; pass it to Restore
	Path        string // where the file was
	Size        int64
	DeletedUnix int64
}

// TrashStore keeps deleted library files for a while.
type TrashStore interface {
	List(ctx context.Context) ([]TrashItem, error)
	// Restore puts a file back where it was; it fails if that place is taken. It returns the path.
	Restore(ctx context.Context, id string) (string, error)
	// Purge permanently removes what was deleted before the given time (the zero time: everything).
	Purge(ctx context.Context, before time.Time) (int, error)
}
