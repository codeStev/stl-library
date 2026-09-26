package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
