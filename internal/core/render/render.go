// Package render draws a preview image of an STL model. It streams the
// file twice - once for the bounds, once to draw - so memory depends on
// the image size only, never on the size of the model. Pure: it reads from
// streams it is given.
package render

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"
)

// Open returns a fresh stream of the model file; Render calls it twice.
type Open func() (io.ReadCloser, error)

// Options for a preview.
type Options struct {
	Size        int // edge length of the square output
	Supersample int // render at Size*Supersample and scale down (anti-aliasing)
}

// Render draws the model from a fixed three-quarter view (Z up, as print
// files are), orthographic, flat shaded, on a transparent background.
func Render(open Open, o Options) (*image.NRGBA, error) {
	if o.Size <= 0 {
		o.Size = 400
	}
	if o.Supersample <= 0 {
		o.Supersample = 2
	}
	cam := newCamera()

	// Pass 1: bounds of the model as seen by the camera.
	lo := vec{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	n, err := each(open, func(t tri) {
		for _, p := range t {
			q := cam.view(p)
			lo, hi = lo.min(q), hi.max(q)
		}
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errors.New("no triangles")
	}

	// Pass 2: rasterize into a depth buffer.
	big := o.Size * o.Supersample
	margin := 0.04 * float64(big)
	scale := (float64(big) - 2*margin) / math.Max(hi.x-lo.x, hi.y-lo.y)
	if math.IsInf(scale, 0) || math.IsNaN(scale) || scale <= 0 {
		return nil, errors.New("degenerate model")
	}
	offX := (float64(big) - (hi.x-lo.x)*scale) / 2
	offY := (float64(big) - (hi.y-lo.y)*scale) / 2
	toScreen := func(p vec) vec {
		q := cam.view(p)
		// Screen y grows downwards; view y grows upwards.
		return vec{offX + (q.x-lo.x)*scale, float64(big) - (offY + (q.y-lo.y)*scale), q.z}
	}
	depth := make([]float32, big*big)
	for i := range depth {
		depth[i] = float32(math.Inf(-1))
	}
	shade := make([]uint8, big*big) // 0 = empty
	light := vec{-0.4, 0.5, 0.75}.norm()
	if _, err := each(open, func(t tri) {
		a, b, c := toScreen(t[0]), toScreen(t[1]), toScreen(t[2])
		// Normal in view space, from the vertices (the file's normals are
		// often missing or wrong).
		nv := cam.dir(t[1].sub(t[0]).cross(t[2].sub(t[0]))).norm()
		if nv.z < 0 {
			nv = nv.scale(-1) // two-sided: broken winding shouldn't punch holes
		}
		lum := 0.28 + 0.72*math.Max(0, nv.dot(light))
		v := uint8(1 + math.Min(254, lum*254))
		raster(a, b, c, big, depth, shade, v)
	}); err != nil {
		return nil, err
	}
	return downsample(shade, big, o.Supersample), nil
}

// raster fills a screen-space triangle with a depth test (larger z is
// nearer the camera).
func raster(a, b, c vec, size int, depth []float32, shade []uint8, v uint8) {
	minX := int(math.Max(0, math.Floor(math.Min(a.x, math.Min(b.x, c.x)))))
	maxX := int(math.Min(float64(size-1), math.Ceil(math.Max(a.x, math.Max(b.x, c.x)))))
	minY := int(math.Max(0, math.Floor(math.Min(a.y, math.Min(b.y, c.y)))))
	maxY := int(math.Min(float64(size-1), math.Ceil(math.Max(a.y, math.Max(b.y, c.y)))))
	area := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
	if area == 0 || minX > maxX || minY > maxY {
		return
	}
	for y := minY; y <= maxY; y++ {
		py := float64(y) + 0.5
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			w0 := ((b.x-px)*(c.y-py) - (b.y-py)*(c.x-px)) / area
			w1 := ((c.x-px)*(a.y-py) - (c.y-py)*(a.x-px)) / area
			w2 := 1 - w0 - w1
			if w0 < 0 || w1 < 0 || w2 < 0 {
				continue
			}
			z := float32(w0*a.z + w1*b.z + w2*c.z)
			i := y*size + x
			if z > depth[i] {
				depth[i] = z
				shade[i] = v
			}
		}
	}
}

// downsample averages ss×ss blocks into the output, with coverage as alpha.
func downsample(shade []uint8, big, ss int) *image.NRGBA {
	size := big / ss
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	base := color.NRGBA{R: 178, G: 186, B: 196} // light blue-grey "resin"
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sum, covered int
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					if s := shade[(y*ss+dy)*big+x*ss+dx]; s > 0 {
						sum += int(s)
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			l := float64(sum) / float64(covered) / 255
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(float64(base.R) * l), G: uint8(float64(base.G) * l), B: uint8(float64(base.B) * l),
				A: uint8(255 * covered / (ss * ss)),
			})
		}
	}
	return out
}

type vec struct{ x, y, z float64 }

func (a vec) sub(b vec) vec       { return vec{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec) scale(s float64) vec { return vec{a.x * s, a.y * s, a.z * s} }
func (a vec) dot(b vec) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec) cross(b vec) vec {
	return vec{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func (a vec) norm() vec {
	l := math.Sqrt(a.dot(a))
	if l == 0 {
		return a
	}
	return a.scale(1 / l)
}
func (a vec) min(b vec) vec { return vec{math.Min(a.x, b.x), math.Min(a.y, b.y), math.Min(a.z, b.z)} }
func (a vec) max(b vec) vec { return vec{math.Max(a.x, b.x), math.Max(a.y, b.y), math.Max(a.z, b.z)} }

type tri [3]vec

// camera: look at the model from the front-right, a little above. View
// space: x right, y up, z towards the viewer.
type camera struct{ r [3]vec }

func newCamera() camera {
	yaw, pitch := -35*math.Pi/180, 25*math.Pi/180
	// Model space is Z up; turn it into Y up, rotate around the vertical
	// axis (yaw), then tilt towards the viewer (pitch).
	cy, sy := math.Cos(yaw), math.Sin(yaw)
	cp, sp := math.Cos(pitch), math.Sin(pitch)
	// Rows of the combined rotation applied to (x, y, z) model coordinates.
	return camera{r: [3]vec{
		{cy, sy, 0},
		{-sy * sp, cy * sp, cp},
		{sy * cp, -cy * cp, sp},
	}}
}

func (c camera) view(p vec) vec { return vec{c.r[0].dot(p), c.r[1].dot(p), c.r[2].dot(p)} }
func (c camera) dir(p vec) vec  { return c.view(p) }

// each streams the triangles of a binary or ASCII STL file.
func each(open Open, fn func(tri)) (int, error) {
	rc, err := open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 1<<16)
	head, err := br.Peek(84)
	if err != nil && len(head) < 5 {
		return 0, fmt.Errorf("reading STL: %w", err)
	}
	if isASCII(br, head) {
		return eachASCII(br, fn)
	}
	return eachBinary(br, fn)
}

// isASCII: starts with "solid" and the first line after it looks like
// text ("facet"). Binary files may also start with "solid" in the header.
func isASCII(br *bufio.Reader, head []byte) bool {
	if !bytes.HasPrefix(bytes.TrimLeft(head, " \t"), []byte("solid")) {
		return false
	}
	peek, _ := br.Peek(512)
	return bytes.Contains(peek, []byte("facet")) || bytes.Contains(peek, []byte("endsolid"))
}

func eachBinary(br *bufio.Reader, fn func(tri)) (int, error) {
	var header [84]byte
	if _, err := io.ReadFull(br, header[:]); err != nil {
		return 0, fmt.Errorf("reading STL header: %w", err)
	}
	count := binary.LittleEndian.Uint32(header[80:])
	var rec [50]byte
	n := 0
	for i := uint32(0); i < count; i++ {
		if _, err := io.ReadFull(br, rec[:]); err != nil {
			if n > 0 && (errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)) {
				return n, nil // truncated file: draw what's there
			}
			return n, fmt.Errorf("reading STL triangle %d: %w", i, err)
		}
		var t tri
		for v := 0; v < 3; v++ {
			o := 12 + v*12
			t[v] = vec{
				float64(math.Float32frombits(binary.LittleEndian.Uint32(rec[o:]))),
				float64(math.Float32frombits(binary.LittleEndian.Uint32(rec[o+4:]))),
				float64(math.Float32frombits(binary.LittleEndian.Uint32(rec[o+8:]))),
			}
		}
		if t.finite() {
			fn(t)
			n++
		}
	}
	return n, nil
}

func eachASCII(br *bufio.Reader, fn func(tri)) (int, error) {
	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var t tri
	v, n := 0, 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 4 || f[0] != "vertex" {
			continue
		}
		var p vec
		var err error
		if p.x, err = strconv.ParseFloat(f[1], 64); err != nil {
			return n, fmt.Errorf("ASCII STL: %w", err)
		}
		if p.y, err = strconv.ParseFloat(f[2], 64); err != nil {
			return n, fmt.Errorf("ASCII STL: %w", err)
		}
		if p.z, err = strconv.ParseFloat(f[3], 64); err != nil {
			return n, fmt.Errorf("ASCII STL: %w", err)
		}
		t[v] = p
		if v++; v == 3 {
			if t.finite() {
				fn(t)
				n++
			}
			v = 0
		}
	}
	return n, sc.Err()
}

func (t tri) finite() bool {
	for _, p := range t {
		for _, c := range []float64{p.x, p.y, p.z} {
			if math.IsNaN(c) || math.IsInf(c, 0) {
				return false
			}
		}
	}
	return true
}
