package app

import (
	"context"
	"path"
	"strings"

	"github.com/codeStev/stl-library/internal/core/library"
)

// SliceItem says a sliced file holds Count copies of a part.
type SliceItem struct {
	PartID int64
	Count  int
}

// PartRef is a part file seen from a link: what it is, in which model,
// how many copies. Missing: the file is not in the library any more (the
// link stays, in case it comes back).
type PartRef struct {
	UploadID  string // set when the file is an uploaded plate
	PartID    int64
	Path      string
	ModelID   int64
	ModelName string
	Count     int
	Missing   bool
}

// SliceInfo is both directions of the links of one part file: what it
// contains (if it is a sliced file) and the sliced files it is in.
type SliceInfo struct {
	Contents []PartRef
	UsedIn   []PartRef
}

const (
	maxSliceItems = 500
	maxSliceCount = 10000
)

// slicedExts are the files that hold a plate: sliced output and the
// slicer projects it comes from.
var slicedExts = map[string]bool{".ctb": true, ".cbddlp": true, ".goo": true, ".chitubox": true, ".lys": true, ".lyt": true, ".photon": true, ".pws": true}

// IsSliced reports whether a file name is a sliced file or slicer project.
func IsSliced(name string) bool { return slicedExts[strings.ToLower(path.Ext(name))] }

// Slices records what sliced files contain.
type Slices struct {
	Store Store
}

// Contents returns the links of a part in both directions.
func (s Slices) Contents(ctx context.Context, partID int64) (SliceInfo, error) {
	return s.Store.SliceContents(ctx, partID)
}

// SetContents replaces what a sliced file contains. The file must be a
// sliced file; a part is listed once (counts add up), counts are 1 or more.
func (s Slices) SetContents(ctx context.Context, sliceID int64, items []SliceItem) error {
	ref, err := s.Store.Part(ctx, sliceID)
	if err != nil {
		return err
	}
	if !IsSliced(ref.Path) || len(items) > maxSliceItems {
		return ErrInvalid
	}
	merged := map[int64]int{}
	var order []int64
	for _, it := range items {
		if it.Count < 1 || it.Count > maxSliceCount || it.PartID == sliceID {
			return ErrInvalid
		}
		if _, ok := merged[it.PartID]; !ok {
			order = append(order, it.PartID)
		}
		merged[it.PartID] += it.Count
	}
	out := make([]SliceItem, 0, len(order))
	for _, id := range order {
		n := merged[id]
		if n > maxSliceCount {
			return ErrInvalid
		}
		out = append(out, SliceItem{PartID: id, Count: n})
	}
	return s.Store.SetSliceContents(ctx, sliceID, out)
}

// UploadContents returns what an uploaded plate contains.
func (s Slices) UploadContents(ctx context.Context, uploadID string) ([]PartRef, error) {
	return s.Store.UploadContents(ctx, uploadID)
}

// SetUploadContents replaces what an uploaded plate contains (same rules as SetContents).
func (s Slices) SetUploadContents(ctx context.Context, uploadID string, items []SliceItem) error {
	if len(items) > maxSliceItems {
		return ErrInvalid
	}
	merged := map[int64]int{}
	var order []int64
	for _, it := range items {
		if it.Count < 1 || it.Count > maxSliceCount {
			return ErrInvalid
		}
		if _, ok := merged[it.PartID]; !ok {
			order = append(order, it.PartID)
		}
		merged[it.PartID] += it.Count
	}
	out := make([]SliceItem, 0, len(order))
	for _, id := range order {
		if merged[id] > maxSliceCount {
			return ErrInvalid
		}
		out = append(out, SliceItem{PartID: id, Count: merged[id]})
	}
	return s.Store.SetUploadContents(ctx, uploadID, out)
}

// VariantCoverage lists, for each part of a variant, the sliced files it is in.
func (s Slices) VariantCoverage(ctx context.Context, variantID int64) (map[int64][]PartRef, error) {
	return s.Store.VariantSlices(ctx, variantID)
}

// SuggestContents proposes the parts a sliced file or slicer project holds, from its file name: parts of the
// same model whose name, without supports/slicer words and scale, is the same ("Arm_left_SUP.lys" holds
// "Arm_left.stl"). Parts in the plate's own variant come first and, when there are any, are the only ones
// returned. Nothing is saved: the user confirms in the editor. (The slicers' files don't carry part names
// in a readable form; .ctb has none at all.)
func (s Slices) SuggestContents(ctx context.Context, plateID int64) ([]PartRef, error) {
	plate, err := s.Store.Part(ctx, plateID)
	if err != nil {
		return nil, err
	}
	key := library.StemKey(path.Base(plate.Path))
	if !IsSliced(plate.Path) || key == "" {
		return []PartRef{}, nil
	}
	vid, err := s.Store.PartVariant(ctx, plateID)
	if err != nil {
		return nil, err
	}
	v, err := s.Store.Variant(ctx, vid)
	if err != nil {
		return nil, err
	}
	m, err := s.Store.Model(ctx, v.ModelID)
	if err != nil {
		return nil, err
	}
	name := m.Name
	if m.DisplayName != "" {
		name = m.DisplayName
	}
	var same, other []PartRef
	for _, mv := range m.Variants {
		for _, p := range mv.Parts {
			if p.ID == plateID || IsSliced(p.Path) || library.StemKey(path.Base(p.Path)) != key {
				continue
			}
			ref := PartRef{PartID: p.ID, Path: p.Path, ModelID: m.ID, ModelName: name, Count: 1}
			if mv.ID == vid {
				same = append(same, ref)
			} else {
				other = append(other, ref)
			}
		}
	}
	if len(same) > 0 {
		return same, nil
	}
	if other == nil {
		other = []PartRef{}
	}
	return other, nil
}
