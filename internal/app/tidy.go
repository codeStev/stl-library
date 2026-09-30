package app

import (
	"context"
	"sync"
	"time"
)

// Kinds of things the cleanup finds.
const (
	TidyJunkFile    = "junk-file"    // .DS_Store, ._name, Thumbs.db ...
	TidyJunkFolder  = "junk-folder"  // __MACOSX (only when nothing but junk is in it)
	TidyEmptyFolder = "empty-folder" // a folder with nothing (but junk) in it
	TidyCopy        = "copy"         // "Head (imported).stl" with exactly the content of "Head.stl"
)

// TidyItem is one thing the cleanup would take care of.
type TidyItem struct {
	Kind     string
	Path     string // below the library root
	Size     int64
	Original string // for a copy: the file it duplicates
}

// LibraryTidier finds and removes leftovers in the library. Junk and empty folders are deleted (they
// hold no model data); a copy is moved to "_duplicates/<path>" (the library's convention for extra
// copies), never deleted.
type LibraryTidier interface {
	Find(ctx context.Context, progress func(folders int)) ([]TidyItem, error)
	// Remove re-checks the item on disk and refuses when it is not (or no longer) what Find said.
	Remove(ctx context.Context, it TidyItem) error
}

// TidyState is the cleanup's current result.
type TidyState struct {
	Running    bool
	Folders    int // folders looked at so far in the current run
	Items      []TidyItem
	FinishedAt int64 // when Items were found, 0 if never
	Error      string
}

// Tidy runs the leftover search in the background and applies what the user confirmed.
type Tidy struct {
	Tidier LibraryTidier
	Now    func() time.Time

	mu    sync.Mutex
	state TidyState
}

func (t *Tidy) now() int64 {
	if t.Now != nil {
		return t.Now().Unix()
	}
	return time.Now().Unix()
}

// State reports the last result and whether a search is running.
func (t *Tidy) State() TidyState {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.state
	s.Items = append([]TidyItem(nil), s.Items...)
	return s
}

// Start searches in the background; it returns false when a search is running already.
func (t *Tidy) Start(ctx context.Context) bool {
	t.mu.Lock()
	if t.state.Running {
		t.mu.Unlock()
		return false
	}
	t.state.Running, t.state.Folders, t.state.Error = true, 0, ""
	t.mu.Unlock()
	go func() {
		items, err := t.Tidier.Find(ctx, func(n int) {
			t.mu.Lock()
			t.state.Folders = n
			t.mu.Unlock()
		})
		t.mu.Lock()
		defer t.mu.Unlock()
		t.state.Running = false
		if err != nil {
			t.state.Error = err.Error()
			return
		}
		t.state.Items, t.state.FinishedAt = items, t.now()
	}()
	return true
}

// TidyResult is the outcome of Apply.
type TidyResult struct {
	Done   int
	Failed []string // "<path>: <why>"
}

// Apply takes care of the listed paths - but only of items the last search found, and only after
// re-checking each on disk. Files come first, then folders deepest first (so a folder that held only
// junk is empty by then).
func (t *Tidy) Apply(ctx context.Context, paths []string) (TidyResult, error) {
	var res TidyResult
	if t.Tidier == nil {
		return res, ErrInvalid
	}
	t.mu.Lock()
	known := map[string]TidyItem{}
	for _, it := range t.state.Items {
		known[it.Path] = it
	}
	t.mu.Unlock()
	var chosen []TidyItem
	for _, p := range paths {
		it, ok := known[p]
		if !ok {
			return res, ErrInvalid
		}
		chosen = append(chosen, it)
	}
	sortTidy(chosen)
	removed := map[string]bool{}
	for _, it := range chosen {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := t.Tidier.Remove(ctx, it); err != nil {
			res.Failed = append(res.Failed, it.Path+": "+err.Error())
			continue
		}
		removed[it.Path] = true
		res.Done++
	}
	t.mu.Lock()
	kept := t.state.Items[:0:0]
	for _, it := range t.state.Items {
		if !removed[it.Path] {
			kept = append(kept, it)
		}
	}
	t.state.Items = kept
	t.mu.Unlock()
	return res, nil
}

func sortTidy(items []TidyItem) {
	rank := func(k string) int {
		if k == TidyEmptyFolder || k == TidyJunkFolder {
			return 1
		}
		return 0
	}
	// stable insertion sort: files before folders, deeper folders before their parents
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a, b := items[j-1], items[j]
			if rank(a.Kind) < rank(b.Kind) || (rank(a.Kind) == rank(b.Kind) && (rank(a.Kind) == 0 || len(a.Path) >= len(b.Path))) {
				break
			}
			items[j-1], items[j] = b, a
		}
	}
}
