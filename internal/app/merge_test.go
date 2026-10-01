package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeMerge struct {
	group   DupGroup
	saved   []string
	failOn  string
	removed []string
}

func (f *fakeMerge) DuplicateGroup(context.Context, string, int64) (DupGroup, error) {
	return f.group, nil
}
func (f *fakeMerge) SaveMerge(_ context.Context, p string, _, _, _ int64) error {
	f.saved = append(f.saved, p)
	return nil
}
func (f *fakeMerge) Link(_ context.Context, keep, other string) (int64, error) {
	if other == f.failOn {
		return 0, errors.New("other disk")
	}
	return 7, nil
}

func (f *fakeMerge) Remove(_ context.Context, keep, other string) error {
	f.removed = append(f.removed, other)
	return nil
}

func TestMergeDuplicatesLinksTheOthersToTheKeptCopy(t *testing.T) {
	f := &fakeMerge{group: DupGroup{Size: 4, Files: []DupFile{{PartID: 1, Path: "a"}, {PartID: 2, Path: "b"}, {PartID: 3, Path: "c", Linked: true}, {PartID: 4, Path: "d"}}}, failOn: "d"}
	n, err := MergeDuplicates(context.Background(), f, f, "x", 4, 1, false, time.Unix(9, 0))
	if n != 1 || err == nil || len(f.saved) != 1 || f.saved[0] != "b" {
		t.Fatalf("merged %d, err %v, saved %v", n, err, f.saved)
	}
	if _, err := MergeDuplicates(context.Background(), f, f, "x", 4, 99, false, time.Unix(9, 0)); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown keep: %v", err)
	}
	n, err = MergeDuplicates(context.Background(), f, f, "x", 4, 1, true, time.Unix(9, 0))
	if n != 3 || err != nil || len(f.removed) != 3 {
		t.Errorf("remove: %d %v %v", n, err, f.removed)
	}
	if w := (DupGroup{Size: 4, Files: []DupFile{{}, {Linked: true}}}).Wasted(); w != 0 {
		t.Errorf("wasted %d", w)
	}
}
