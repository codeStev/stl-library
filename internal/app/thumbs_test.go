package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	convention "github.com/codeStev/stl-convention"
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

func TestRenderPartPrefersOnePieceThenUnsupportedThenAnything(t *testing.T) {
	v := func(sup, split string, parts ...FileRef) VariantDetail {
		return VariantDetail{Dims: convention.Dims{Supports: sup, Split: split}, Parts: parts}
	}
	f := func(p string, size int64) FileRef { return FileRef{Path: p, Size: size} }
	cases := []struct {
		m    ModelDetail
		want string
	}{
		{ModelDetail{Variants: []VariantDetail{
			v("Supported", "", f("s/body.stl", 900)),
			v("No Supports", "", f("n/arm.stl", 50), f("n/body.stl", 300)),
			v("", "Combined", f("c/whole.stl", 100)),
		}}, "c/whole.stl"},
		{ModelDetail{Variants: []VariantDetail{
			v("Supported", "", f("s/body.stl", 900)),
			v("No Supports", "", f("n/arm.stl", 50), f("n/body.stl", 300), f("n/big.lys", 5000)),
		}}, "n/body.stl"},
		{ModelDetail{Variants: []VariantDetail{v("Supported", "", f("s/a.stl", 1), f("s/b.STL", 2))}}, "s/b.STL"},
		{ModelDetail{Variants: []VariantDetail{v("Supported", "", f("s/a.lys", 1))}}, ""},
	}
	for i, c := range cases {
		got := RenderPart(&c.m)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%d: got %s, want none", i, got.Path)
		case c.want != "" && (got == nil || got.Path != c.want):
			t.Errorf("%d: got %v, want %s", i, got, c.want)
		}
	}
}

type renderStore struct{ Store }

func (renderStore) Model(context.Context, int64) (*ModelDetail, error) {
	return &ModelDetail{Variants: []VariantDetail{{Parts: []FileRef{{Path: "C/M/tri.stl", Size: 134}}}}}, nil
}

func TestModelPreviewFallsBackToARenderedSTL(t *testing.T) {
	// One binary-STL triangle.
	var stl bytes.Buffer
	stl.Write(make([]byte, 80))
	binary.Write(&stl, binary.LittleEndian, uint32(1))
	binary.Write(&stl, binary.LittleEndian, [12]float32{0, 0, 0, 0, 0, 0, 10, 0, 0, 0, 0, 10})
	stl.Write([]byte{0, 0})
	th := NewThumbs(renderStore{}, &countingFiles{data: stl.Bytes()}, memCache{})
	data, ct, err := th.Model(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if ct != "image/png" {
		t.Errorf("content type %q", ct)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("not a PNG: %v", err)
	}
}
