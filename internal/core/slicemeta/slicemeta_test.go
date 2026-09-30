package slicemeta

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"image"
	"image/png"
	"math"
	"os"
	"testing"
)

func put32(b []byte, o int, v uint32) { binary.LittleEndian.PutUint32(b[o:], v) }
func putF(b []byte, o int, v float32) { put32(b, o, math.Float32bits(v)) }

// rle codes a picture of runs: red for the first half, blue for the rest (RGB15 + repeat count).
func rle(w, h int) []byte {
	var out []byte
	run := func(r, g, b uint16, n int) {
		dot := r<<11 | g<<6 | b
		if n == 1 {
			out = binary.LittleEndian.AppendUint16(out, dot)
			return
		}
		out = binary.LittleEndian.AppendUint16(out, dot|0x20)
		out = binary.LittleEndian.AppendUint16(out, uint16(n-1))
	}
	half := w * h / 2
	run(31, 0, 0, half)
	run(0, 0, 31, w*h-half)
	return out
}

func encrypted(t *testing.T) []byte {
	const settingsOff, size = 48, 288
	file := make([]byte, 4096)
	put32(file, 0, magicCTBEnc)
	put32(file, 4, size)
	put32(file, 8, settingsOff)
	put32(file, 16, 5)
	s := make([]byte, size)
	putF(s, 12, 218.88)
	putF(s, 16, 122.88)
	putF(s, 20, 220)
	putF(s, 36, 0.05)
	putF(s, 40, 2.5)
	putF(s, 44, 35)
	put32(s, 52, 5)
	put32(s, 56, 11520)
	put32(s, 60, 5120)
	put32(s, 64, 1975)
	put32(s, 68, 400) // large preview
	put32(s, 76, 12279)
	putF(s, 104, 44.97)
	blk, _ := aes.NewCipher(secretKey)
	cipher.NewCBCEncrypter(blk, secretIV).CryptBlocks(file[settingsOff:settingsOff+size], s)
	data := rle(8, 4)
	put32(file, 400, 8)
	put32(file, 404, 4)
	put32(file, 408, 416)
	put32(file, 412, uint32(len(data)))
	copy(file[416:], data)
	return file
}

func TestEncryptedCTBv5(t *testing.T) {
	m, err := Parse(encrypted(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != "ctb" || m.Version != 5 || m.Layers != 1975 || m.PrintSeconds != 12279 || m.BottomLayers != 5 ||
		m.ResX != 11520 || m.ResY != 5120 || m.LayerHeight != 0.05 || m.ExposureS != 2.5 || m.BottomExposureS != 35 ||
		m.BedX != 218.88 || m.BedZ != 220 || m.VolumeMl != 44.97 {
		t.Errorf("%+v", m)
	}
	img := decode(t, m)
	if r, _, b, _ := img.At(0, 0).RGBA(); r>>8 != 248 || b != 0 {
		t.Errorf("the first half is red, got %d,%d", r>>8, b>>8)
	}
	if _, _, b, _ := img.At(7, 3).RGBA(); b>>8 != 248 {
		t.Errorf("the last pixel is blue, got %d", b>>8)
	}
}

func decode(t *testing.T, m *Meta) image.Image {
	t.Helper()
	data, err := m.PreviewPNG()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != m.previewW || img.Bounds().Dy() != m.previewH {
		t.Fatalf("size %v", img.Bounds())
	}
	return img
}

func TestPlainCTBAndCBDDLP(t *testing.T) {
	for _, magic := range []uint32{magicCTB, magicCBDDLP, magicCTBv4} {
		f := make([]byte, 2048)
		put32(f, 0, magic)
		put32(f, 4, 3)
		putF(f, 8, 192)
		putF(f, 12, 120)
		putF(f, 16, 245)
		putF(f, 32, 0.05)
		putF(f, 36, 2)
		putF(f, 40, 30)
		put32(f, 48, 6)
		put32(f, 52, 7680)
		put32(f, 56, 4320)
		put32(f, 60, 300) // large preview
		put32(f, 68, 1200)
		put32(f, 76, 3600)
		put32(f, 84, 500) // print parameters
		putF(f[500:], 20, 12.5)
		data := rle(8, 4)
		put32(f, 300, 8)
		put32(f, 304, 4)
		put32(f, 308, 340)
		put32(f, 312, uint32(len(data)))
		copy(f[340:], data)
		m, err := Parse(f)
		if err != nil {
			t.Fatalf("%#x: %v", magic, err)
		}
		if m.Layers != 1200 || m.PrintSeconds != 3600 || m.ResX != 7680 || m.BottomLayers != 6 || m.VolumeMl != 12.5 || m.LayerHeight != 0.05 || !m.HasPreview() {
			t.Errorf("%#x: %+v", magic, m)
		}
		if _, err := m.PreviewPNG(); err != nil {
			t.Errorf("%#x preview: %v", magic, err)
		}
	}
}

func TestGarbageAndTruncatedFilesAreRefused(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":   nil,
		"text":    []byte("this is not a sliced file, just some text that is long enough to pass the size check.."),
		"zeros":   make([]byte, 1000),
		"short":   encrypted(t)[:60],
		"badkey":  func() []byte { b := encrypted(t); b[60] ^= 0xff; return b }(),
		"noplate": func() []byte { b := make([]byte, 200); put32(b, 0, magicCTB); return b }(),
	} {
		m, err := Parse(b)
		if name == "badkey" {
			// a damaged block decrypts to nonsense: refused, never a wrong answer
			if err == nil && (m.ResX != 11520 || m.Layers != 1975) {
				t.Errorf("%s: accepted nonsense %+v", name, m)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A preview that points outside the prefix is just missing.
	b := encrypted(t)
	m, err := Parse(b[:410])
	if err == nil && m.HasPreview() {
		t.Error("a cut-off preview must not be used")
	}
}

func TestRealChituboxFile(t *testing.T) {
	const f = "/home/codestev/stl/Gwen Stacy - Into the Spider-Verse/2.ctb"
	fh, err := os.Open(f)
	if err != nil {
		t.Skip("no sample file")
	}
	defer fh.Close()
	b := make([]byte, PrefixSize)
	n, _ := fh.Read(b)
	m, err := Parse(b[:n])
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 5 || m.Layers != 1975 || m.ResX != 11520 || m.LayerHeight != 0.05 || m.ExposureS != 2.5 || !m.HasPreview() {
		t.Errorf("%+v", m)
	}
	if _, err := m.PreviewPNG(); err != nil {
		t.Error(err)
	}
}
