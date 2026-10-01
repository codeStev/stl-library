package disk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/app"
)

// TrashDir is where deleted files wait, inside the library root. Like every top-level folder
// starting with "_", the scanner leaves it alone. Each deletion gets a folder named by the time
// (in nanoseconds), below it the file under its old path.
const TrashDir = "_trash"

// LibraryTrash moves files out of the library into TrashDir and back.
type LibraryTrash struct {
	Root string
}

func (t LibraryTrash) full(rel string) (string, error) { return LibraryWriter{Root: t.Root}.full(rel) }

// Put moves the library file rel into the trash.
func (t LibraryTrash) Put(rel string, now time.Time) error {
	src, err := t.full(rel)
	if err != nil {
		return err
	}
	dst, err := t.full(path.Join(TrashDir, strconv.FormatInt(now.UnixNano(), 10), rel))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func (t LibraryTrash) List(ctx context.Context) ([]app.TrashItem, error) {
	base := filepath.Join(t.Root, TrashDir)
	stamps, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []app.TrashItem
	for _, st := range stamps {
		ns, err := strconv.ParseInt(st.Name(), 10, 64)
		if err != nil || !st.IsDir() {
			continue
		}
		root := filepath.Join(base, st.Name())
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			out = append(out, app.TrashItem{ID: st.Name() + "/" + rel, Path: rel, Size: info.Size(), DeletedUnix: ns / 1e9})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeletedUnix != out[j].DeletedUnix {
			return out[i].DeletedUnix > out[j].DeletedUnix
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (t LibraryTrash) Restore(_ context.Context, id string) (string, error) {
	stamp, rel, ok := strings.Cut(id, "/")
	if _, err := strconv.ParseInt(stamp, 10, 64); !ok || err != nil {
		return "", fmt.Errorf("%w: unknown trash entry", app.ErrInvalid)
	}
	src, err := t.full(path.Join(TrashDir, stamp, rel))
	if err != nil {
		return "", err
	}
	dst, err := t.full(rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(src); err != nil {
		return "", app.ErrNotFound
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("%w: %s exists again", app.ErrInvalid, rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	t.prune(filepath.Dir(src), filepath.Join(t.Root, TrashDir))
	return rel, nil
}

// prune removes empty folders from dir up to (not including) stop.
func (t LibraryTrash) prune(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func (t LibraryTrash) Purge(ctx context.Context, before time.Time) (int, error) {
	base := filepath.Join(t.Root, TrashDir)
	stamps, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, st := range stamps {
		ns, err := strconv.ParseInt(st.Name(), 10, 64)
		if err != nil || !st.IsDir() || (!before.IsZero() && ns >= before.UnixNano()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return n, err
		}
		dir := filepath.Join(base, st.Name())
		files := 0
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files++
			}
			return nil
		})
		if err := os.RemoveAll(dir); err != nil {
			return n, err
		}
		n += files
	}
	return n, nil
}
