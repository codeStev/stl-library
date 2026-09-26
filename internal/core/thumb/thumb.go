// Package thumb makes small preview images. Pure: it reads and writes
// streams, never files.
package thumb

import (
	"fmt"
	"image"
	_ "image/gif" // registers the decoder
	"image/jpeg"
	_ "image/png"
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// FromImage decodes an image (JPEG, PNG, GIF, WebP) and writes a JPEG that
// fits into size×size, keeping the aspect ratio. Images already smaller
// are re-encoded as they are.
func FromImage(r io.Reader, w io.Writer, size int) error {
	src, _, err := image.Decode(r)
	if err != nil {
		return fmt.Errorf("decoding image: %w", err)
	}
	b := src.Bounds()
	width, height := fit(b.Dx(), b.Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	// White background for transparent PNGs, which JPEG can't express.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return jpeg.Encode(w, dst, &jpeg.Options{Quality: 82})
}

func fit(w, h, size int) (int, int) {
	if w <= size && h <= size {
		return max(w, 1), max(h, 1)
	}
	if w >= h {
		return size, max(h*size/w, 1)
	}
	return max(w*size/h, 1), size
}
