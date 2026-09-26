package thumb

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestShrinksKeepingTheAspectRatio(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1200, 600))
	for x := 0; x < 1200; x++ {
		for y := 0; y < 600; y++ {
			src.Set(x, y, color.NRGBA{200, 50, 50, 255})
		}
	}
	var in, out bytes.Buffer
	png.Encode(&in, src)
	if err := FromImage(&in, &out, 400); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(&out)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 200 {
		t.Errorf("size %v", b)
	}
}

func TestSmallImagesAreNotEnlargedAndGarbageIsAnError(t *testing.T) {
	var in, out bytes.Buffer
	png.Encode(&in, image.NewNRGBA(image.Rect(0, 0, 50, 80)))
	if err := FromImage(&in, &out, 400); err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(&out)
	if b := img.Bounds(); b.Dx() != 50 || b.Dy() != 80 {
		t.Errorf("size %v", b)
	}
	if err := FromImage(bytes.NewReader([]byte("not an image")), &out, 400); err == nil {
		t.Error("garbage decoded")
	}
}
