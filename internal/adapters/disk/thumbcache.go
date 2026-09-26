package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// ThumbCache stores thumbnails as files in Dir, one per key.
type ThumbCache struct {
	Dir string
}

func (c ThumbCache) path(key string) string { return filepath.Join(c.Dir, key[:2], key+".jpg") }

func (c ThumbCache) Get(key string) ([]byte, bool, error) {
	data, err := os.ReadFile(c.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// Put writes atomically (temp file + rename), so a crash never leaves a
// half-written thumbnail behind.
func (c ThumbCache) Put(key string, data []byte) error {
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

var reKey = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// Keys lists the cached keys (only files named like one).
func (c ThumbCache) Keys() ([]string, error) {
	var out []string
	dirs, err := os.ReadDir(c.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() || len(d.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(c.Dir, d.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if reKey.MatchString(f.Name()) && f.Name()[:2] == d.Name() {
				out = append(out, f.Name()[:32])
			}
		}
	}
	return out, nil
}

func (c ThumbCache) Delete(key string) error {
	if !reKey.MatchString(key + ".jpg") {
		return fmt.Errorf("not a thumbnail key: %q", key)
	}
	err := os.Remove(c.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
