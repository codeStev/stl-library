package app

import (
	"context"
	"io"

	"github.com/codeStev/stl-library/internal/core/slicemeta"
)

// SliceMeta reads what sliced files say about themselves (preview picture, layers,
// print time, resin) - from library files and from uploaded plates.
type SliceMeta struct {
	Store  Store
	Files  Files
	Plates PlateFiles
}

func readPrefix(rc io.Reader) (*slicemeta.Meta, error) {
	b, err := io.ReadAll(io.LimitReader(rc, slicemeta.PrefixSize))
	if err != nil {
		return nil, err
	}
	return slicemeta.Parse(b)
}

// OfPart reads a sliced file of the library.
func (s SliceMeta) OfPart(ctx context.Context, partID int64) (*slicemeta.Meta, error) {
	ref, err := s.Store.Part(ctx, partID)
	if err != nil {
		return nil, err
	}
	if !IsSliced(ref.Path) {
		return nil, ErrInvalid
	}
	rc, err := s.Files.Open(ctx, ref.Path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readPrefix(rc)
}

// OfUpload reads an uploaded plate.
func (s SliceMeta) OfUpload(ctx context.Context, uploadID string) (*slicemeta.Meta, error) {
	if _, err := s.Store.Upload(ctx, uploadID); err != nil {
		return nil, err
	}
	f, _, err := s.Plates.Open(ctx, uploadID)
	if err != nil {
		return nil, ErrNotFound
	}
	defer f.Close()
	return readPrefix(f)
}
