package app

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

// BulkEdit changes where several models live: another creator, release or category (nil: unchanged; for
// release and category, "" takes the level away) and/or a find-and-replace in the folder names.
type BulkEdit struct {
	IDs                        []int64
	Creator, Release, Category *string
	Find, Replace              string
}

// BulkRow is what the edit would do with one model.
type BulkRow struct {
	ID       int64
	Name     string
	From, To string
	Problem  string // why this model is left alone; "" when it would be moved
}

// MaxBulk is how many models one edit may touch.
const MaxBulk = 500

// Bulk moves and renames the folders of several models at once. Every move is journaled like a fix
// (and can be undone there), and the tags, prints, collections and so on follow the model.
type Bulk struct {
	Store     Store
	Editor    LibraryEditor
	Keys      KeyMover
	Dirs      ModelDirs
	Overrides PreviewOverrides
	Now       func() time.Time
}

func (b Bulk) now() int64 {
	if b.Now != nil {
		return b.Now().Unix()
	}
	return time.Now().Unix()
}

// validFolder says whether s can be a folder name at one level of the library.
func validFolder(s string) bool {
	if s == "" || s != strings.TrimSpace(s) || utf8.RuneCountInString(s) > 120 || s == "." || s == ".." ||
		strings.HasPrefix(s, ".") || strings.HasPrefix(s, "_") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(`/\<>:"|?*`, r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

func joinLevels(levels ...string) string {
	var parts []string
	for _, l := range levels {
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, "/")
}

// Plan works out, without touching anything, what the edit would do.
func (b Bulk) Plan(ctx context.Context, e BulkEdit) ([]BulkRow, error) {
	if len(e.IDs) == 0 || len(e.IDs) > MaxBulk {
		return nil, fmt.Errorf("%w: choose 1 to %d models", ErrInvalid, MaxBulk)
	}
	if e.Creator == nil && e.Release == nil && e.Category == nil && e.Find == "" {
		return nil, fmt.Errorf("%w: nothing to change", ErrInvalid)
	}
	if e.Creator != nil && !validFolder(*e.Creator) {
		return nil, fmt.Errorf("%w: the creator is not a usable folder name", ErrInvalid)
	}
	for _, l := range []*string{e.Release, e.Category} {
		if l != nil && *l != "" && !validFolder(*l) {
			return nil, fmt.Errorf("%w: %q is not a usable folder name", ErrInvalid, *l)
		}
	}
	rows := make([]BulkRow, 0, len(e.IDs))
	targets := map[string]int{} // target -> index of the first row that wants it
	for _, id := range e.IDs {
		m, err := b.Store.Model(ctx, id)
		if err != nil {
			return nil, err
		}
		row := BulkRow{ID: id, Name: m.Name, From: m.Dir}
		if m.DisplayName != "" {
			row.Name = m.DisplayName
		}
		creator, release, category := m.Creator, m.Release, m.Category
		name := path.Base(m.Dir)
		if joinLevels(creator, release, category, name) != m.Dir {
			row.Problem = "unusual folder layout - left alone"
			rows = append(rows, row)
			continue
		}
		if e.Creator != nil {
			creator = *e.Creator
		}
		if e.Release != nil {
			release = *e.Release
		}
		if e.Category != nil {
			category = *e.Category
		}
		if e.Find != "" {
			name = strings.TrimSpace(strings.ReplaceAll(name, e.Find, e.Replace))
		}
		row.To = joinLevels(creator, release, category, name)
		switch {
		case row.To == row.From:
			row.Problem = "nothing changes"
		case !validFolder(name):
			row.Problem = "the new name is not usable"
		case strings.HasPrefix(row.To, row.From+"/"):
			row.Problem = "cannot move a folder into itself"
		}
		if row.Problem == "" {
			if first, dup := targets[row.To]; dup {
				row.Problem = fmt.Sprintf("same target as %s", rows[first].From)
			} else {
				targets[row.To] = len(rows)
			}
		}
		if row.Problem == "" {
			if _, exists, err := b.Editor.Stat(ctx, row.To); err != nil {
				return nil, err
			} else if exists {
				row.Problem = "a folder of that name exists already"
			}
		}
		if row.Problem == "" && b.Dirs != nil {
			for anc := path.Dir(row.To); anc != "."; anc = path.Dir(anc) {
				if is, err := b.Dirs.IsModelDir(ctx, anc); err != nil {
					return nil, err
				} else if is {
					row.Problem = "the target lies inside another model"
					break
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// BulkResult is the outcome of Apply.
type BulkResult struct {
	Done   int
	Failed []string
	Rows   []BulkRow
}

// Apply does the moves the plan shows as free of problems; the others are left alone.
func (b Bulk) Apply(ctx context.Context, e BulkEdit) (BulkResult, error) {
	var res BulkResult
	if b.Editor == nil {
		return res, ErrInvalid
	}
	rows, err := b.Plan(ctx, e)
	if err != nil {
		return res, err
	}
	res.Rows = rows
	fx := Fixes{Store: b.Store, Editor: b.Editor, Keys: b.Keys, Overrides: b.Overrides, Now: b.Now}
	for _, r := range rows {
		if r.Problem != "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := b.Editor.Move(ctx, r.From, r.To); err != nil {
			res.Failed = append(res.Failed, r.From+": "+err.Error())
			continue
		}
		if err := fx.follow(ctx, r.From, r.To); err != nil {
			res.Failed = append(res.Failed, r.From+": "+err.Error())
			continue
		}
		if _, err := b.Store.AddFix(ctx, r.From, r.To, b.now()); err != nil {
			res.Failed = append(res.Failed, r.From+": moved, but not journaled: "+err.Error())
		}
		res.Done++
	}
	return res, nil
}
