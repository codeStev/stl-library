package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	convention "github.com/codeStev/stl-convention"
)

type zipStore struct{ Store }

func (zipStore) Variant(context.Context, int64) (*VariantDetail, error) {
	return &VariantDetail{ID: 7, ModelID: 3, Dims: convention.Dims{Scale: "32mm", Supports: "Supported", Format: "Lychee"}, Option: "Helmet Version",
		Parts: []FileRef{{Path: "C/R/Bell Head/32mm/Supported Lychee/Helmet Version/a.lys"}, {Path: "C/R/Bell Head/32mm/Supported Lychee/Helmet Version/b.lys"}}}, nil
}

func (zipStore) Model(context.Context, int64) (*ModelDetail, error) {
	return &ModelDetail{ModelSummary: ModelSummary{ID: 3, Name: "Bell Head"}}, nil
}

type mapFiles map[string]string

func (m mapFiles) Open(_ context.Context, p string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(m[p])), nil
}

func TestVariantZipStoresThePartsUnderTheirNames(t *testing.T) {
	var buf bytes.Buffer
	var name string
	files := mapFiles{
		"C/R/Bell Head/32mm/Supported Lychee/Helmet Version/a.lys": "AAAA",
		"C/R/Bell Head/32mm/Supported Lychee/Helmet Version/b.lys": "BB",
	}
	if err := VariantZip(context.Background(), zipStore{}, files, 7, func(n string) { name = n }, &buf); err != nil {
		t.Fatal(err)
	}
	if name != "Bell Head - 32mm Supported Lychee Helmet Version.zip" {
		t.Errorf("name %q", name)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s compressed (method %d)", f.Name, f.Method)
		}
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		got[f.Name] = string(b)
	}
	if len(got) != 2 || got["a.lys"] != "AAAA" || got["b.lys"] != "BB" {
		t.Errorf("zip contents %v", got)
	}
}
