package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// PreviewStore keeps chosen model previews as PNG files in Dir (the app's data), one per key.
type PreviewStore struct{ Dir string }

var rePreviewKey = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s PreviewStore) path(key string) (string, error) {
	if !rePreviewKey.MatchString(key) {
		return "", errors.New("bad preview key")
	}
	return filepath.Join(s.Dir, key+".png"), nil
}

func (s PreviewStore) Get(key string) ([]byte, int64, bool, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, 0, false, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	data, err := os.ReadFile(p)
	return data, info.ModTime().Unix(), err == nil, err
}

// Put writes atomically.
func (s PreviewStore) Put(key string, data []byte) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
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

func (s PreviewStore) Delete(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s PreviewStore) Stat(key string) (int64, bool) {
	p, err := s.path(key)
	if err != nil {
		return 0, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	return info.ModTime().Unix(), true
}
