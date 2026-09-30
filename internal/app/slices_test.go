package app

import (
	"context"
	"testing"
)

type suggestStore struct {
	Store
	m ModelDetail
}

func (s suggestStore) Part(_ context.Context, id int64) (*FileRef, error) {
	for _, v := range s.m.Variants {
		for _, p := range v.Parts {
			if p.ID == id {
				return &p, nil
			}
		}
	}
	return nil, ErrNotFound
}
func (s suggestStore) PartVariant(_ context.Context, id int64) (int64, error) {
	for _, v := range s.m.Variants {
		for _, p := range v.Parts {
			if p.ID == id {
				return v.ID, nil
			}
		}
	}
	return 0, ErrNotFound
}
func (s suggestStore) Variant(_ context.Context, id int64) (*VariantDetail, error) {
	for _, v := range s.m.Variants {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, ErrNotFound
}
func (s suggestStore) Model(context.Context, int64) (*ModelDetail, error) { return &s.m, nil }

func TestSuggestContentsMatchesPartNamesPreferringThePlatesOwnVariant(t *testing.T) {
	m := ModelDetail{Variants: []VariantDetail{
		{ID: 1, ModelID: 7, Parts: []FileRef{{ID: 10, Path: "C/M/Supported/Arm_left_SUP.lys"}, {ID: 11, Path: "C/M/Supported/Arm_left.stl"}, {ID: 12, Path: "C/M/Supported/Head.stl"}}},
		{ID: 2, ModelID: 7, Parts: []FileRef{{ID: 20, Path: "C/M/No Supports/Arm_left.stl"}, {ID: 21, Path: "C/M/No Supports/Head.stl"}}},
		{ID: 3, ModelID: 7, Parts: []FileRef{{ID: 30, Path: "C/M/Other/Head_SUP.lys"}}},
	}}
	m.ID, m.Name = 7, "Model"
	s := Slices{Store: suggestStore{m: m}}
	ctx := context.Background()
	got, err := s.SuggestContents(ctx, 10)
	if err != nil || len(got) != 1 || got[0].PartID != 11 || got[0].ModelID != 7 {
		t.Errorf("same variant first: %+v %v", got, err)
	}
	got, _ = s.SuggestContents(ctx, 30) // no Head in its own variant: the other variants' Heads
	if len(got) != 2 || got[0].PartID != 12 && got[1].PartID != 12 {
		t.Errorf("fallback: %+v", got)
	}
	got, _ = s.SuggestContents(ctx, 11) // not a sliced file
	if len(got) != 0 {
		t.Errorf("a stl got suggestions: %+v", got)
	}
}
