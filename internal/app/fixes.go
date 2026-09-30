package app

import (
	"context"
	"path"
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

// LibraryEditor renames folders of the library. It is the only way the app ever changes the
// library, and only ever one folder name at a time.
type LibraryEditor interface {
	Stat(ctx context.Context, rel string) (size int64, exists bool, err error)
	// Rename renames a folder within its parent; it refuses to replace anything.
	Rename(ctx context.Context, from, to string) error
}

// Fixes suggests and applies renames of folders that don't follow the convention.
type Fixes struct {
	Store  Store
	Editor LibraryEditor
	Now    func() time.Time
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
	if !ok || path.Dir(from) != path.Dir(to) {
		return 0, ErrInvalid
	}
	if err := f.Editor.Rename(ctx, from, to); err != nil {
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
	if err := f.Editor.Rename(ctx, rec.To, rec.From); err != nil {
		return err
	}
	return f.Store.MarkFixUndone(ctx, id)
}

// Journal lists the applied renames, newest first.
func (f Fixes) Journal(ctx context.Context) ([]FixRecord, error) { return f.Store.Fixes(ctx) }
