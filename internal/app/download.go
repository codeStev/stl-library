package app

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	convention "github.com/codeStev/stl-convention"
)

// Files opens library files for reading, by path relative to the library
// root. Implementations must refuse paths that leave the root.
type Files interface {
	Open(ctx context.Context, path string) (io.ReadCloser, error)
}

// VariantZip streams the parts of a variant as a zip archive to w. Files
// are stored, not compressed: print files hardly compress and the server's
// CPU is better spent elsewhere. It returns the suggested file name first,
// before anything is written, so a caller can set headers.
func VariantZip(ctx context.Context, s Store, f Files, id int64, name func(string), w io.Writer) error {
	v, err := s.Variant(ctx, id)
	if err != nil {
		return err
	}
	m, err := s.Model(ctx, v.ModelID)
	if err != nil {
		return err
	}
	name(zipName(m.Name, v))
	zw := zip.NewWriter(w)
	for _, p := range v.Parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := addFile(ctx, zw, f, p); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(ctx context.Context, zw *zip.Writer, f Files, p FileRef) error {
	r, err := f.Open(ctx, p.Path)
	if err != nil {
		return err
	}
	defer r.Close()
	dst, err := zw.CreateHeader(&zip.FileHeader{Name: path.Base(p.Path), Method: zip.Store})
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, r)
	return err
}

// zipName: "Bell Head - 32mm Supported Lychee.zip".
func zipName(model string, v *VariantDetail) string {
	label := strings.Join(convention.CanonicalSegments(v.Dims), " ")
	if v.Option != "" {
		label = strings.TrimSpace(label + " " + v.Option)
	}
	if label == "" {
		return fmt.Sprintf("%s.zip", model)
	}
	return fmt.Sprintf("%s - %s.zip", model, label)
}
