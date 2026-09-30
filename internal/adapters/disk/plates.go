package disk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/codeStev/stl-library/internal/app"
)

// PlateStore keeps uploaded plates in a folder of the app's data, named by
// their sha256 - the library itself is never written.
type PlateStore struct{ Dir string }

var reHexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Save stores r (at most max bytes) and returns its id and size.
func (s PlateStore) Save(_ context.Context, r io.Reader, max int64) (string, int64, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(s.Dir, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, max+1))
	if err != nil {
		return "", 0, err
	}
	if n > max {
		return "", 0, app.ErrTooLarge
	}
	if n == 0 {
		return "", 0, fmt.Errorf("empty upload")
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	id := hex.EncodeToString(h.Sum(nil))
	p := filepath.Join(s.Dir, id)
	if _, err := os.Stat(p); err == nil {
		return id, n, nil // the same content is stored already
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", 0, err
	}
	done = true
	return id, n, nil
}

// Open returns an uploaded file.
func (s PlateStore) Open(_ context.Context, id string) (io.ReadSeekCloser, int64, error) {
	if !reHexID.MatchString(id) {
		return nil, 0, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.Dir, id))
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}
