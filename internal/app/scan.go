package app

import (
	"context"
	"errors"

	convention "github.com/codeStev/stl-convention"
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
	PartVariant(ctx context.Context, partID int64) (int64, error)

	// User data, keyed by folder so it survives rescans.
	SetTags(ctx context.Context, modelID int64, tags []string) error
	// EditTags adds and removes tags on many models at once, leaving their
	// other tags alone.
	EditTags(ctx context.Context, modelIDs []int64, add, remove []string) error
	SetDisplayName(ctx context.Context, modelID int64, name string) error
	Tags(ctx context.Context) ([]TagCount, error)
	AddPrint(ctx context.Context, variantID int64, atUnix int64, note string) (Print, error)
	DeletePrint(ctx context.Context, id int64) error
	SetHidden(ctx context.Context, modelID int64, hidden bool) error
	SetVariantLabel(ctx context.Context, variantID int64, label *VariantLabel) error
	Enqueue(ctx context.Context, variantID int64, atUnix int64, note string) error
	Dequeue(ctx context.Context, variantID int64) error
	Queue(ctx context.Context) ([]QueueItem, error)

	// What a sliced file contains, and where a part is used.
	SliceContents(ctx context.Context, partID int64) (SliceInfo, error)
	SetSliceContents(ctx context.Context, partID int64, items []SliceItem) error
	VariantSlices(ctx context.Context, variantID int64) (map[int64][]PartRef, error)
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
	Tag     string
	Printed *bool // only printed (true) or never printed (false) models
	Hidden  bool  // include hidden models
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
	Cover    int64 // id of the model's first image (jpg/png/webp/gif), 0 if none
	// Renderable: the model has an STL a preview can be rendered from.
	Renderable bool
	// User data.
	DisplayName string // replaces Name for display when set
	Tags        []string
	Prints      int // how often a variant of it was printed
	Hidden      bool
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
	Prints  []Print
	Queued  bool
	// Relabeled: Dims and Option come from a correction, not the folders.
	Relabeled bool
}

// VariantLabel is a correction of a variant's dimensions and option.
type VariantLabel struct {
	Dims   convention.Dims
	Option string
}

// Print records that a variant was printed.
type Print struct {
	ID      int64
	AtUnix  int64
	Note    string
	Variant string // variant folder
}

// TagCount is a tag with the number of models carrying it.
type TagCount struct {
	Tag    string
	Models int
}

// QueueItem is a variant waiting to be printed.
type QueueItem struct {
	VariantID int64
	ModelID   int64
	Model     string // display name or name
	Label     string // variant levels and option, e.g. "32mm Supported Lychee"
	AddedUnix int64
	Note      string
}

// FileRef is a file of the library by id and path.
type FileRef struct {
	ID      int64
	Path    string
	Size    int64
	ModUnix int64
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
