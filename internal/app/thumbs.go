package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"io"
	"strings"

	"github.com/codeStev/stl-library/internal/core/render"
	"github.com/codeStev/stl-library/internal/core/thumb"
)

// ThumbCache keeps generated thumbnails by key.
type ThumbCache interface {
	Get(key string) ([]byte, bool, error)
	Put(key string, data []byte) error
}

// ThumbSize is the edge length thumbnails fit into.
const ThumbSize = 400

// Thumbs makes and caches image thumbnails. Only one is generated at a
// time: decoding a large image takes a lot of memory on a small server.
type Thumbs struct {
	Store Store
	Files Files
	Cache ThumbCache
	gen   chan struct{}
}

func NewThumbs(s Store, f Files, c ThumbCache) *Thumbs {
	return &Thumbs{Store: s, Files: f, Cache: c, gen: make(chan struct{}, 1)}
}

// ErrNoPreview means a model has neither an image nor an STL to render.
var ErrNoPreview = fmt.Errorf("no image or STL to preview: %w", ErrNotFound)

// Model returns a preview of a model: the thumbnail of its cover image,
// or else a render of one of its STL files. Renders are PNGs (transparent
// background), image thumbnails JPEGs; the content type is returned too.
func (t *Thumbs) Model(ctx context.Context, id int64) ([]byte, string, error) {
	m, err := t.Store.Model(ctx, id)
	if err != nil {
		return nil, "", err
	}
	var imgErr error
	if m.Cover != 0 {
		data, err := t.Image(ctx, m.Cover)
		if err == nil || ctx.Err() != nil {
			return data, "image/jpeg", err
		}
		imgErr = err // unreadable image: try a render instead
	}
	part := RenderPart(m)
	if part == nil {
		if imgErr != nil {
			return nil, "", imgErr
		}
		return nil, "", ErrNoPreview
	}
	data, _, err := t.render(ctx, *part)
	return data, "image/png", err
}

// RenderPart picks the STL that shows a model best: a one-piece file if
// there is one, else the largest part of an unsupported variant (supports
// hide the model), else the largest STL of any variant.
func RenderPart(m *ModelDetail) *FileRef {
	var combined, unsupported, any *FileRef
	for _, v := range m.Variants {
		for i := range v.Parts {
			p := &v.Parts[i]
			if !strings.EqualFold(pathExt(p.Path), "stl") {
				continue
			}
			larger := func(cur *FileRef) *FileRef {
				if cur == nil || p.Size > cur.Size {
					return p
				}
				return cur
			}
			any = larger(any)
			if v.Dims.Supports != "Supported" {
				unsupported = larger(unsupported)
				if v.Dims.Split == "Combined" {
					combined = larger(combined)
				}
			}
		}
	}
	switch {
	case combined != nil:
		return combined
	case unsupported != nil:
		return unsupported
	}
	return any
}

func pathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.Contains(p[i:], "/") {
		return p[i+1:]
	}
	return ""
}

func (t *Thumbs) render(ctx context.Context, part FileRef) ([]byte, bool, error) {
	key := cacheKey("render", part.Path, part.Size, part.ModUnix)
	return t.cached(ctx, key, func(w io.Writer) error {
		img, err := render.Render(func() (io.ReadCloser, error) { return t.Files.Open(ctx, part.Path) },
			render.Options{Size: ThumbSize})
		if err != nil {
			return err
		}
		return png.Encode(w, img)
	})
}

func cacheKey(kind, path string, size, mod int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%d", kind, path, size, mod, ThumbSize)))
	return hex.EncodeToString(sum[:16])
}

// cached returns the cached entry for key, or makes it with gen - one
// generation at a time - and caches it. fresh reports a new entry.
func (t *Thumbs) cached(ctx context.Context, key string, gen func(io.Writer) error) (data []byte, fresh bool, err error) {
	if data, ok, err := t.Cache.Get(key); err != nil || ok {
		return data, false, err
	}
	select {
	case t.gen <- struct{}{}:
		defer func() { <-t.gen }()
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	if data, ok, err := t.Cache.Get(key); err != nil || ok { // made while waiting
		return data, false, err
	}
	var buf bytes.Buffer
	if err := gen(&buf); err != nil {
		return nil, false, err
	}
	if err := t.Cache.Put(key, buf.Bytes()); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

// WarmCovers makes the missing thumbnails of every model's cover, one at a
// time, so the library grid is fast. Returns how many were made and how
// many failed (unreadable images).
func (t *Thumbs) WarmCovers(ctx context.Context) (made, failed int) {
	models, err := t.Store.Search(ctx, Query{Limit: 1 << 30})
	if err != nil {
		return 0, 0
	}
	for _, m := range models {
		if m.Cover == 0 || ctx.Err() != nil {
			continue
		}
		fresh, err := t.image(ctx, m.Cover)
		switch {
		case err != nil:
			failed++
		case fresh:
			made++
		}
	}
	return made, failed
}

// WarmRenders renders the missing previews of models without an image.
// Rendering reads the whole STL twice, so this is the slow part; it runs
// after WarmCovers.
func (t *Thumbs) WarmRenders(ctx context.Context) (made, failed int) {
	models, err := t.Store.Search(ctx, Query{Limit: 1 << 30})
	if err != nil {
		return 0, 0
	}
	for _, s := range models {
		if s.Cover != 0 || !s.Renderable || ctx.Err() != nil {
			continue
		}
		m, err := t.Store.Model(ctx, s.ID)
		if err != nil {
			failed++
			continue
		}
		part := RenderPart(m)
		if part == nil {
			continue
		}
		_, fresh, err := t.render(ctx, *part)
		switch {
		case err != nil:
			failed++
		case fresh:
			made++
		}
	}
	return made, failed
}

// Image returns a JPEG thumbnail of an image of the library.
func (t *Thumbs) Image(ctx context.Context, id int64) ([]byte, error) {
	data, _, err := t.get(ctx, id)
	return data, err
}

func (t *Thumbs) image(ctx context.Context, id int64) (fresh bool, err error) {
	_, fresh, err = t.get(ctx, id)
	return fresh, err
}

// get returns the thumbnail and whether it was generated just now. The
// key covers the image's path, size and modification time, so a changed
// image gets a new thumbnail.
func (t *Thumbs) get(ctx context.Context, id int64) ([]byte, bool, error) {
	img, err := t.Store.Image(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return t.cached(ctx, cacheKey("image", img.Path, img.Size, img.ModUnix), func(w io.Writer) error {
		r, err := t.Files.Open(ctx, img.Path)
		if err != nil {
			return err
		}
		defer r.Close()
		return thumb.FromImage(r, w, ThumbSize)
	})
}
