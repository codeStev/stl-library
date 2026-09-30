package disk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/library"
)

// LibraryTidier finds leftovers in a library root and removes them on request. Top-level folders that
// start with "_" (like "_duplicates") and the importer's hidden temporary folders are left alone.
type LibraryTidier struct {
	Root string
}

// DuplicatesDir is where copies are moved to.
const DuplicatesDir = "_duplicates"

func (t LibraryTidier) full(rel string) (string, error) {
	return LibraryWriter{Root: t.Root}.full(rel)
}

func (t LibraryTidier) Find(ctx context.Context, progress func(int)) ([]app.TidyItem, error) {
	var out []app.TidyItem
	folders := 0
	var walk func(rel string) (empty bool, err error)
	walk = func(rel string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		dir := t.Root
		if rel != "" {
			dir = filepath.Join(t.Root, filepath.FromSlash(rel))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false, err
		}
		folders++
		if progress != nil && folders%50 == 0 {
			progress(folders)
		}
		empty := true
		byName := map[string]fs.DirEntry{}
		for _, e := range entries {
			byName[e.Name()] = e
		}
		for _, e := range entries {
			name := e.Name()
			child := path.Join(rel, name)
			switch {
			case e.IsDir():
				if rel == "" && (strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")) && !library.IsJunkFolder(name) {
					empty = false // not ours to look into
					continue
				}
				if strings.HasPrefix(name, ".stlib-import") {
					empty = false
					continue
				}
				if library.IsJunkFolder(name) {
					if onlyJunk(filepath.Join(dir, name)) {
						out = append(out, app.TidyItem{Kind: app.TidyJunkFolder, Path: child})
						continue
					}
					empty = false
					continue
				}
				sub, err := walk(child)
				if err != nil {
					return false, err
				}
				if sub {
					out = append(out, app.TidyItem{Kind: app.TidyEmptyFolder, Path: child})
				} else {
					empty = false
				}
			case library.IsJunkFile(name):
				var size int64
				if info, err := e.Info(); err == nil {
					size = info.Size()
				}
				out = append(out, app.TidyItem{Kind: app.TidyJunkFile, Path: child, Size: size})
			case strings.HasPrefix(name, "."):
				empty = false // someone else's hidden file (e.g. an import in progress)
			default:
				empty = false
				if !e.Type().IsRegular() {
					continue
				}
				orig, ok := library.CopyOf(name)
				if !ok {
					continue
				}
				o, ok := byName[orig]
				if !ok || !o.Type().IsRegular() {
					continue
				}
				a, errA := e.Info()
				b, errB := o.Info()
				if errA != nil || errB != nil || a.Size() != b.Size() || a.Size() == 0 {
					continue
				}
				same, err := sameContent(filepath.Join(dir, name), filepath.Join(dir, orig))
				if err != nil {
					return false, err
				}
				if same {
					out = append(out, app.TidyItem{Kind: app.TidyCopy, Path: child, Size: a.Size(), Original: path.Join(rel, orig)})
				}
			}
		}
		return empty, nil
	}
	if _, err := walk(""); err != nil {
		return nil, err
	}
	if progress != nil {
		progress(folders)
	}
	return out, nil
}

// onlyJunk reports whether everything below dir is junk files in junk-free folders.
func onlyJunk(dir string) bool {
	ok := true
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || (!d.IsDir() && !library.IsJunkFile(d.Name())) {
			ok = false
			return filepath.SkipAll
		}
		return nil
	})
	return ok
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sameContent(a, b string) (bool, error) {
	ha, err := fileSHA(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

// Remove re-checks an item against the disk and then takes care of it.
func (t LibraryTidier) Remove(ctx context.Context, it app.TidyItem) error {
	p, err := t.full(it.Path)
	if err != nil {
		return err
	}
	name := path.Base(it.Path)
	switch it.Kind {
	case app.TidyJunkFile:
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || !library.IsJunkFile(name) {
			return errors.New("not a junk file any more")
		}
		return os.Remove(p)
	case app.TidyJunkFolder:
		if !library.IsJunkFolder(name) || !onlyJunk(p) {
			return errors.New("holds more than junk")
		}
		return os.RemoveAll(p)
	case app.TidyEmptyFolder:
		// remove what is left of junk, then the folder itself - os.Remove refuses a folder with anything in it
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !library.IsJunkFile(e.Name()) {
				return errors.New("is not empty")
			}
		}
		for _, e := range entries {
			if err := os.Remove(filepath.Join(p, e.Name())); err != nil {
				return err
			}
		}
		return os.Remove(p)
	case app.TidyCopy:
		orig, ok := library.CopyOf(name)
		if !ok || path.Join(path.Dir(it.Path), orig) != it.Original {
			return errors.New("not a copy any more")
		}
		op, err := t.full(it.Original)
		if err != nil {
			return err
		}
		same, err := sameContent(p, op)
		if err != nil {
			return err
		}
		if !same {
			return errors.New("its content differs from the original")
		}
		dst, err := t.full(path.Join(DuplicatesDir, it.Path))
		if err != nil {
			return err
		}
		if _, err := os.Lstat(dst); err == nil {
			return ErrExists
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Rename(p, dst)
	}
	return app.ErrInvalid
}
