package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

// cube returns the 12 triangles of an axis-aligned cube of edge s.
func cube(s float64) []tri {
	p := func(x, y, z float64) vec { return vec{x * s, y * s, z * s} }
	q := [][4]vec{
		{p(0, 0, 0), p(1, 0, 0), p(1, 1, 0), p(0, 1, 0)},
		{p(0, 0, 1), p(1, 0, 1), p(1, 1, 1), p(0, 1, 1)},
		{p(0, 0, 0), p(1, 0, 0), p(1, 0, 1), p(0, 0, 1)},
		{p(0, 1, 0), p(1, 1, 0), p(1, 1, 1), p(0, 1, 1)},
		{p(0, 0, 0), p(0, 1, 0), p(0, 1, 1), p(0, 0, 1)},
		{p(1, 0, 0), p(1, 1, 0), p(1, 1, 1), p(1, 0, 1)},
	}
	var out []tri
	for _, f := range q {
		out = append(out, tri{f[0], f[1], f[2]}, tri{f[0], f[2], f[3]})
	}
	return out
}

func binarySTL(ts []tri, headerSolid bool) []byte {
	var b bytes.Buffer
	h := make([]byte, 80)
	if headerSolid {
		copy(h, "solid binary file with a text-looking header")
	}
	b.Write(h)
	binary.Write(&b, binary.LittleEndian, uint32(len(ts)))
	for _, t := range ts {
		binary.Write(&b, binary.LittleEndian, [3]float32{}) // normal, ignored
		for _, v := range t {
			binary.Write(&b, binary.LittleEndian, [3]float32{float32(v.x), float32(v.y), float32(v.z)})
		}
		b.Write([]byte{0, 0})
	}
	return b.Bytes()
}

func asciiSTL(ts []tri) []byte {
	var b strings.Builder
	b.WriteString("solid cube\n")
	for _, t := range ts {
		b.WriteString("  facet normal 0 0 0\n    outer loop\n")
		for _, v := range t {
			fmt.Fprintf(&b, "      vertex %g %g %g\n", v.x, v.y, v.z)
		}
		b.WriteString("    endloop\n  endfacet\n")
	}
	b.WriteString("endsolid cube\n")
	return []byte(b.String())
}

func opener(data []byte, opens *int) Open {
	return func() (io.ReadCloser, error) {
		*opens++
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func coverage(t *testing.T, data []byte) (float64, int) {
	t.Helper()
	opens := 0
	img, err := Render(opener(data, &opens), Options{Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 100 || img.Bounds().Dy() != 100 {
		t.Fatalf("size %v", img.Bounds())
	}
	covered, shades := 0, map[uint8]bool{}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] == 255 {
			covered++
			shades[img.Pix[i-3]] = true
		}
	}
	if opens != 2 {
		t.Errorf("file opened %d times, want 2 (streaming passes)", opens)
	}
	return float64(covered) / 10000, len(shades)
}

func TestRendersACubeFromBinaryAndASCII(t *testing.T) {
	for name, data := range map[string][]byte{
		"binary":               binarySTL(cube(10), false),
		"binary, solid header": binarySTL(cube(10), true),
		"ascii":                asciiSTL(cube(10)),
	} {
		cov, shades := coverage(t, data)
		// A cube seen from above-front-right covers a good part of the
		// frame, and three faces show in three different shades.
		if cov < 0.3 || cov > 0.9 {
			t.Errorf("%s: coverage %.2f", name, cov)
		}
		if shades < 3 {
			t.Errorf("%s: %d shades, want the three visible faces distinct", name, shades)
		}
	}
}

func TestScaleOfTheModelDoesNotMatter(t *testing.T) {
	a, _ := coverage(t, binarySTL(cube(0.5), false))
	b, _ := coverage(t, binarySTL(cube(5000), false))
	if math.Abs(a-b) > 0.02 {
		t.Errorf("coverage differs with scale: %.3f vs %.3f", a, b)
	}
}

func TestEmptyAndBrokenFilesAreErrors(t *testing.T) {
	opens := 0
	for name, data := range map[string][]byte{
		"empty":        {},
		"no triangles": binarySTL(nil, false),
		"flat":         binarySTL([]tri{{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}}}, false),
	} {
		if _, err := Render(opener(data, &opens), Options{Size: 50}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTruncatedBinaryDrawsWhatIsThere(t *testing.T) {
	data := binarySTL(cube(10), false)
	opens := 0
	if _, err := Render(opener(data[:len(data)-30], &opens), Options{Size: 50}); err != nil {
		t.Errorf("truncated: %v", err)
	}
}
