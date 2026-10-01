package disk

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LibraryLinker merges identical files into hard links.
type LibraryLinker struct {
	Root string
}

// Link replaces the file other by a hard link to keep, after reading both and finding the
// same content. The link is made under a temporary name and renamed over other, so other is
// never missing. It returns the modification time the file has afterwards. Hard links work
// only within one file system.
func (l LibraryLinker) Link(ctx context.Context, keep, other string) (int64, error) {
	w := LibraryWriter{Root: l.Root}
	kp, err := w.full(keep)
	if err != nil {
		return 0, err
	}
	op, err := w.full(other)
	if err != nil {
		return 0, err
	}
	ki, err := os.Lstat(kp)
	if err != nil {
		return 0, err
	}
	oi, err := os.Lstat(op)
	if err != nil {
		return 0, err
	}
	if !ki.Mode().IsRegular() || !oi.Mode().IsRegular() {
		return 0, fmt.Errorf("%s: not a regular file", other)
	}
	if os.SameFile(ki, oi) {
		return oi.ModTime().Unix(), nil // linked already
	}
	if ki.Size() != oi.Size() {
		return 0, fmt.Errorf("%s: not the same size as %s", other, keep)
	}
	keepSum, err := fileSum(ctx, kp)
	if err != nil {
		return 0, err
	}
	otherSum, err := fileSum(ctx, op)
	if err != nil {
		return 0, err
	}
	if keepSum != otherSum {
		return 0, fmt.Errorf("%s: content differs from %s", other, keep)
	}
	tmp := filepath.Join(filepath.Dir(op), ".link-"+filepath.Base(op)+".tmp")
	_ = os.Remove(tmp)
	if err := os.Link(kp, tmp); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, op); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	fi, err := os.Stat(op)
	if err != nil {
		return 0, err
	}
	return fi.ModTime().Unix(), nil
}

func fileSum(ctx context.Context, p string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	f, err := os.Open(p)
	if err != nil {
		return out, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return out, err
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
