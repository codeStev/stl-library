package app

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/core/library"
)

// FixRecord is a folder rename that was applied from a suggestion.
type FixRecord struct {
	ID       int64
	From, To string
	AtUnix   int64
	Undone   bool
}

// LibraryEditor renames and moves folders of the library. With the cleanup of leftovers and the
// importer's new files, it is the only way the app ever changes the library - and only for changes the
// user confirmed.
type LibraryEditor interface {
	Stat(ctx context.Context, rel string) (size int64, exists bool, err error)
	// Move moves a folder to another parent and/or name, creating the parent folders; it refuses to
	// replace anything.
	Move(ctx context.Context, from, to string) error
}

// KeyMover follows a moved folder in the index and in everything keyed by its path (tags, prints,
// queue, collections, labels, plates, hashes), so nothing people attached to it is lost.
type KeyMover interface {
	MoveKeys(ctx context.Context, from, to string) error
}

// ModelDirs tells whether a folder is a model's folder in the index.
type ModelDirs interface {
	IsModelDir(ctx context.Context, dir string) (bool, error)
}

// Fixes suggests and applies renames of folders that don't follow the convention.
type Fixes struct {
	Store  Store
	Editor LibraryEditor
	// Keys (optional) carries user data along with a renamed or moved folder; Overrides (optional)
	// carries a model's chosen preview picture.
	Keys      KeyMover
	Overrides PreviewOverrides
	Now       func() time.Time
}

// follow carries the data attached to a folder along after it was moved on disk. When that fails, the
// folder is moved back.
func (f Fixes) follow(ctx context.Context, from, to string) error {
	if f.Keys != nil {
		if err := f.Keys.MoveKeys(ctx, from, to); err != nil {
			_ = f.Editor.Move(ctx, to, from)
			return err
		}
	}
	moveOverride(f.Overrides, from, to)
	return nil
}

// moveOverride re-keys a model's chosen preview picture (a missing one is nothing to do).
func moveOverride(o PreviewOverrides, from, to string) {
	if o == nil {
		return
	}
	if data, _, ok, err := o.Get(overrideKey(from)); err == nil && ok {
		if o.Put(overrideKey(to), data) == nil {
			_ = o.Delete(overrideKey(from))
		}
	}
}

func (f Fixes) now() int64 {
	if f.Now != nil {
		return f.Now().Unix()
	}
	return time.Now().Unix()
}

// Suggestions lists the renames that would clear entries of the issue list, leaving out those whose
// target name is taken already.
func (f Fixes) Suggestions(ctx context.Context) ([]library.Fix, error) {
	issues, err := f.Store.Issues(ctx)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(issues))
	for _, i := range issues {
		dirs = append(dirs, i.Dir)
	}
	var out []library.Fix
	for _, fix := range library.Suggest(dirs) {
		if f.Editor != nil {
			if _, taken, err := f.Editor.Stat(ctx, fix.To); err != nil || taken {
				continue
			}
		}
		out = append(out, fix)
	}
	return out, nil
}

// Apply renames a folder - but only to what is currently suggested for it.
func (f Fixes) Apply(ctx context.Context, from, to string) (int64, error) {
	if f.Editor == nil {
		return 0, ErrInvalid
	}
	sug, err := f.Suggestions(ctx)
	if err != nil {
		return 0, err
	}
	ok := false
	for _, s := range sug {
		ok = ok || (s.From == from && s.To == to)
	}
	if !ok || !strings.HasPrefix(to, path.Dir(from)+"/") {
		return 0, ErrInvalid
	}
	// a rename, or a split into the canonical levels below the same parent
	if err := f.Editor.Move(ctx, from, to); err != nil {
		return 0, err
	}
	if err := f.follow(ctx, from, to); err != nil {
		return 0, err
	}
	return f.Store.AddFix(ctx, from, to, f.now())
}

// Undo renames a folder back - if nothing else took its place meanwhile.
func (f Fixes) Undo(ctx context.Context, id int64) error {
	rec, err := f.Store.Fix(ctx, id)
	if err != nil {
		return err
	}
	if rec.Undone || f.Editor == nil {
		return ErrInvalid
	}
	// a bulk edit may have moved the folder to another parent: Move covers renames as well
	if err := f.Editor.Move(ctx, rec.To, rec.From); err != nil {
		return err
	}
	if err := f.follow(ctx, rec.To, rec.From); err != nil {
		return err
	}
	return f.Store.MarkFixUndone(ctx, id)
}

// Journal lists the applied renames, newest first.
func (f Fixes) Journal(ctx context.Context) ([]FixRecord, error) { return f.Store.Fixes(ctx) }
