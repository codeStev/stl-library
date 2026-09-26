package app

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"testing"
)

type imgStore struct{ Store }

func (imgStore) Image(context.Context, int64) (*FileRef, error) {
	return &FileRef{ID: 1, Path: "C/R/M/cover.png", Size: 3, ModUnix: 9}, nil
}

type countingFiles struct {
	data  []byte
	opens int
}

func (f *countingFiles) Open(context.Context, string) (io.ReadCloser, error) {
	f.opens++
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

type memCache map[string][]byte

func (m memCache) Get(k string) ([]byte, bool, error) { d, ok := m[k]; return d, ok, nil }
func (m memCache) Put(k string, d []byte) error       { m[k] = d; return nil }

func TestThumbnailIsMadeOnceThenServedFromTheCache(t *testing.T) {
	var src bytes.Buffer
	png.Encode(&src, image.NewNRGBA(image.Rect(0, 0, 800, 800)))
	files := &countingFiles{data: src.Bytes()}
	th := NewThumbs(imgStore{}, files, memCache{})
	for i := 0; i < 2; i++ {
		data, err := th.Image(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds().Dx() != ThumbSize {
			t.Fatalf("thumb: %v %v", err, img.Bounds())
		}
	}
	if files.opens != 1 {
		t.Errorf("original opened %d times", files.opens)
	}
}

func (imgStore) Search(context.Context, Query) ([]ModelSummary, error) {
	return []ModelSummary{{ID: 1, Cover: 1}, {ID: 2}, {ID: 3, Cover: 1}}, nil
}

func TestWarmCoversMakesMissingThumbnailsOnce(t *testing.T) {
	var src bytes.Buffer
	png.Encode(&src, image.NewNRGBA(image.Rect(0, 0, 10, 10)))
	th := NewThumbs(imgStore{}, &countingFiles{data: src.Bytes()}, memCache{})
	if made, failed := th.WarmCovers(context.Background()); made != 1 || failed != 0 {
		t.Errorf("first warm: made %d failed %d", made, failed)
	}
	if made, _ := th.WarmCovers(context.Background()); made != 0 {
		t.Errorf("second warm made %d", made)
	}
}
