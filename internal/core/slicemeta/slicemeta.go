// Package slicemeta reads what a sliced file says about itself: the preview picture
// and the print settings (layers, time, resin) of Chitubox files - .ctb (v2 to v5,
// including the encrypted v4/v5 files) and .cbddlp. It is pure: it works on the first
// bytes of the file (see PrefixSize), never on the disk.
//
// The layouts were worked out by the open-source slicer community (UVtools, PrusaSlicer
// discussions); only the header and preview are read, never the layers.
package slicemeta

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// PrefixSize is how much of the start of a file Parse needs: the headers and the
// previews sit right behind each other at the beginning.
const PrefixSize = 4 << 20

// Meta is what a sliced file says about itself. Fields the format doesn't have are zero.
type Meta struct {
	Format           string // "ctb", "cbddlp"
	Version          int
	Layers           int
	LayerHeight      float32 // mm
	ExposureS        float32
	BottomExposureS  float32
	BottomLayers     int
	PrintSeconds     int
	VolumeMl         float32
	ResX, ResY       int
	BedX, BedY, BedZ float32 // mm

	preview            []byte // run-length coded picture, RGB15
	previewW, previewH int
}

// HasPreview says whether the file has a picture Parse could read.
func (m *Meta) HasPreview() bool { return len(m.preview) > 0 && m.previewW > 0 && m.previewH > 0 }

// ErrUnsupported is returned for files that are not a known sliced format.
var ErrUnsupported = errors.New("not a supported sliced file")

const (
	magicCBDDLP = 0x12FD0019
	magicCTB    = 0x12FD0086
	magicCTBv4  = 0x12FD0106 // unencrypted v4
	magicCTBEnc = 0x12FD0107 // encrypted v4/v5
)

// Parse reads the start of a sliced file.
func Parse(b []byte) (*Meta, error) {
	if len(b) < 64 {
		return nil, ErrUnsupported
	}
	switch binary.LittleEndian.Uint32(b) {
	case magicCBDDLP, magicCTB, magicCTBv4:
		return parsePlain(b)
	case magicCTBEnc:
		return parseEncrypted(b)
	}
	return nil, ErrUnsupported
}

func u32(b []byte, o int) uint32 {
	if o < 0 || o+4 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint32(b[o:])
}

func f32(b []byte, o int) float32 { return math.Float32frombits(u32(b, o)) }

// sane drops garbage floats.
func sane(f float32) float32 {
	if f != f || f < 0 || f > 1e7 {
		return 0
	}
	return f
}

// parsePlain reads the unencrypted header of .cbddlp and .ctb (up to v3).
func parsePlain(b []byte) (*Meta, error) {
	m := &Meta{Format: "ctb", Version: int(u32(b, 4))}
	if u32(b, 0) == magicCBDDLP {
		m.Format = "cbddlp"
	}
	m.BedX, m.BedY, m.BedZ = sane(f32(b, 8)), sane(f32(b, 12)), sane(f32(b, 16))
	m.LayerHeight = sane(f32(b, 32))
	m.ExposureS, m.BottomExposureS = sane(f32(b, 36)), sane(f32(b, 40))
	m.BottomLayers = int(u32(b, 48))
	m.ResX, m.ResY = int(u32(b, 52)), int(u32(b, 56))
	largePreview := int(u32(b, 60))
	m.Layers = int(u32(b, 68))
	m.PrintSeconds = int(u32(b, 76))
	if m.ResX <= 0 || m.ResY <= 0 || m.ResX > 100000 || m.ResY > 100000 || m.Layers <= 0 || m.Layers > 1000000 {
		return nil, ErrUnsupported
	}
	if off := int(u32(b, 84)); off > 0 && off+32 <= len(b) { // print parameters (v2 and later)
		if v := sane(f32(b, off+20)); v > 0 && v < 100000 {
			m.VolumeMl = v
		}
	}
	// The preview: 32 bytes (width, height, data offset, data length, ...), then the data.
	if largePreview > 0 {
		readPreview(m, b, largePreview)
	}
	return m, nil
}

func readPreview(m *Meta, b []byte, at int) {
	w, h := int(u32(b, at)), int(u32(b, at+4))
	off, n := int(u32(b, at+8)), int(u32(b, at+12))
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 || n <= 0 || off <= 0 || off+n > len(b) {
		return
	}
	m.previewW, m.previewH, m.preview = w, h, b[off:off+n]
}

// The keys of the encrypted v4/v5 files. They are not secret: every slicer reading these
// files uses the same two constants (stored here in the usual disguised form).
var (
	secretKey = mustXOR("hQ36XB6yTk+zO02ysyiowt8yC1buK+nbLWyfY40EXoU=")
	secretIV  = mustXOR("Wld+ampndVJecmVjYH5cWQ==")
)

func mustXOR(b64 string) []byte {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		panic(err)
	}
	const k = "UVtools"
	for i := range raw {
		raw[i] ^= k[i%len(k)]
	}
	return raw
}

// parseEncrypted reads a v4/v5 file: a 48-byte header, then the settings block encrypted
// with AES-256-CBC (no padding).
func parseEncrypted(b []byte) (*Meta, error) {
	size, off := int(u32(b, 4)), int(u32(b, 8))
	if size < 160 || size > 4096 || size%aes.BlockSize != 0 || off < 48 || off+size > len(b) {
		return nil, ErrUnsupported
	}
	blk, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, err
	}
	s := make([]byte, size)
	cipher.NewCBCDecrypter(blk, secretIV).CryptBlocks(s, b[off:off+size])
	m := &Meta{Format: "ctb", Version: int(u32(b, 16))}
	m.BedX, m.BedY, m.BedZ = sane(f32(s, 12)), sane(f32(s, 16)), sane(f32(s, 20))
	m.LayerHeight = sane(f32(s, 36))
	m.ExposureS, m.BottomExposureS = sane(f32(s, 40)), sane(f32(s, 44))
	m.BottomLayers = int(u32(s, 52))
	m.ResX, m.ResY = int(u32(s, 56)), int(u32(s, 60))
	m.Layers = int(u32(s, 64))
	m.PrintSeconds = int(u32(s, 76))
	m.VolumeMl = sane(f32(s, 104))
	if m.ResX <= 0 || m.ResY <= 0 || m.ResX > 100000 || m.ResY > 100000 || m.Layers <= 0 || m.Layers > 1000000 {
		return nil, fmt.Errorf("%w: the settings do not decrypt", ErrUnsupported)
	}
	if large := int(u32(s, 68)); large > 0 {
		readPreview(m, b, large)
	}
	return m, nil
}

// PreviewPNG decodes the preview picture.
func (m *Meta) PreviewPNG() ([]byte, error) {
	if !m.HasPreview() {
		return nil, errors.New("no preview")
	}
	img := image.NewRGBA(image.Rect(0, 0, m.previewW, m.previewH))
	total := m.previewW * m.previewH
	px := 0
	d := m.preview
	for i := 0; i+1 < len(d) && px < total; i += 2 {
		dot := uint16(d[i]) | uint16(d[i+1])<<8
		c := color.RGBA{uint8(dot>>11&0x1f) << 3, uint8(dot>>6&0x1f) << 3, uint8(dot&0x1f) << 3, 255}
		run := 1
		if dot&0x20 != 0 { // a repeat count follows (12 bits)
			if i+3 >= len(d) {
				break
			}
			run += int(d[i+2]) | int(d[i+3]&0x0f)<<8
			i += 2
		}
		for ; run > 0 && px < total; run-- {
			img.SetRGBA(px%m.previewW, px/m.previewW, c)
			px++
		}
	}
	if px < total/2 {
		return nil, errors.New("the preview is damaged")
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
