package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

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

// Image returns a JPEG thumbnail of an image of the library. The cache key
// covers the image's path, size and modification time, so a changed image
// gets a new thumbnail.
func (t *Thumbs) Image(ctx context.Context, id int64) ([]byte, error) {
	data, _, err := t.get(ctx, id)
	return data, err
}

func (t *Thumbs) image(ctx context.Context, id int64) (fresh bool, err error) {
	_, fresh, err = t.get(ctx, id)
	return fresh, err
}

// get returns the thumbnail and whether it was generated just now.
func (t *Thumbs) get(ctx context.Context, id int64) ([]byte, bool, error) {
	img, err := t.Store.Image(ctx, id)
	if err != nil {
		return nil, false, err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", img.Path, img.Size, img.ModUnix, ThumbSize)))
	key := hex.EncodeToString(sum[:16])
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
	r, err := t.Files.Open(ctx, img.Path)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if err := thumb.FromImage(r, &buf, ThumbSize); err != nil {
		return nil, false, err
	}
	if err := t.Cache.Put(key, buf.Bytes()); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}
