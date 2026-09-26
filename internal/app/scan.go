package app

import (
	"context"
	"errors"

	"github.com/codeStev/stl-library/convention"
	"github.com/codeStev/stl-library/internal/core/library"
)

// Store keeps the library index. Sync makes it match a fresh read of the
// library, rewriting only what changed: a model keeps its identity (and
// with it everything attached to it later) as long as its folder stays.
type Store interface {
	Sync(ctx context.Context, models []*library.Model, issues []library.Issue) (SyncStats, error)
	Search(ctx context.Context, q Query) ([]ModelSummary, error)
	Model(ctx context.Context, id int64) (*ModelDetail, error)
	Creators(ctx context.Context) ([]CreatorCount, error)
	Issues(ctx context.Context) ([]library.Issue, error)
	Variant(ctx context.Context, id int64) (*VariantDetail, error)
	Image(ctx context.Context, id int64) (*FileRef, error)
	Part(ctx context.Context, id int64) (*FileRef, error)
}

// ErrNotFound is returned by a Store for an unknown id.
var ErrNotFound = errors.New("not found")

// SyncStats counts what a sync changed.
type SyncStats struct {
	Added, Updated, Removed, Unchanged int
	Issues                             int
}

// Query searches models. Text is matched against name, creator, release
// and category (all words must match, prefixes count); empty lists all.
type Query struct {
	Text    string
	Creator string
	Limit   int
	Offset  int
}

// ModelSummary is one search hit.
type ModelSummary struct {
	ID       int64
	Creator  string
	Release  string
	Category string
	Name     string
	Dir      string
	Variants int
	Parts    int
	Bytes    int64
}

// ModelDetail is a model with its variants and images.
type ModelDetail struct {
	ModelSummary
	Variants []VariantDetail
	Images   []FileRef
}

// VariantDetail is one variant with its part files.
type VariantDetail struct {
	ID      int64
	ModelID int64
	Dims    convention.Dims
	Option  string
	Dir     string
	Parts   []FileRef
}

// FileRef is a file of the library by id and path.
type FileRef struct {
	ID   int64
	Path string
	Size int64
}

// CreatorCount is a creator with its number of models.
type CreatorCount struct {
	Name   string
	Models int
}

// Scan reads the library and brings the index up to date.
func Scan(ctx context.Context, l Lister, s Store) (SyncStats, error) {
	files, err := l.List(ctx)
	if err != nil {
		return SyncStats{}, err
	}
	models, issues := library.Read(files)
	return s.Sync(ctx, models, issues)
}

// Search finds models in the index.
func Search(ctx context.Context, s Store, q Query) ([]ModelSummary, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	return s.Search(ctx, q)
}
