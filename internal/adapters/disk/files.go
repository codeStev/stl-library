package disk

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Files opens library files for reading. Paths are relative to Root in
// slash form; anything that would leave Root is refused.
type Files struct {
	Root string
}

// ErrOutsideRoot is returned for paths that would leave the library root.
var ErrOutsideRoot = errors.New("path outside the library root")

func (f Files) Open(_ context.Context, rel string) (io.ReadCloser, error) {
	clean := path.Clean("/" + rel)
	if rel == "" || clean != "/"+rel || strings.Contains(rel, "\\") {
		return nil, ErrOutsideRoot
	}
	return os.Open(filepath.Join(f.Root, filepath.FromSlash(clean[1:])))
}

func timeUnix(s int64) time.Time { return time.Unix(s, 0) }
