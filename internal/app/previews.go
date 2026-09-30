package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
)

// PreviewOverrides stores the pictures people chose as the preview of a model
// (from the 3D viewer), outside the library.
type PreviewOverrides interface {
	Get(key string) (data []byte, modUnix int64, ok bool, err error)
	Put(key string, data []byte) error
	Delete(key string) error
	// Stat says when the override was made, without reading it.
	Stat(key string) (modUnix int64, ok bool)
}

// overrideKey names a model's override by its folder, so it survives rescans.
func overrideKey(dir string) string {
	sum := sha256.Sum256([]byte("preview|" + dir))
	return hex.EncodeToString(sum[:16])
}

const maxPreviewBytes = 4 << 20

// Previews sets and resets the chosen preview picture of a model.
type Previews struct {
	Store     Store
	Overrides PreviewOverrides
}

// Set stores a PNG (16 to 4096 px each way, at most 4 MiB) as the model's preview.
func (p Previews) Set(ctx context.Context, modelID int64, data []byte) error {
	if len(data) == 0 || len(data) > maxPreviewBytes || p.Overrides == nil {
		return ErrInvalid
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 4096 || cfg.Height > 4096 {
		return ErrInvalid
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return ErrInvalid
	}
	m, err := p.Store.Model(ctx, modelID)
	if err != nil {
		return err
	}
	return p.Overrides.Put(overrideKey(m.Dir), data)
}

// Reset goes back to the model's own image or render.
func (p Previews) Reset(ctx context.Context, modelID int64) error {
	m, err := p.Store.Model(ctx, modelID)
	if err != nil {
		return err
	}
	if p.Overrides == nil {
		return nil
	}
	return p.Overrides.Delete(overrideKey(m.Dir))
}

// Version is when a model's chosen preview was made (0: it has none). It
// changes with every new choice, so a browser can tell a stale picture.
func (p Previews) Version(dir string) int64 {
	if p.Overrides == nil {
		return 0
	}
	v, _ := p.Overrides.Stat(overrideKey(dir))
	return v
}
