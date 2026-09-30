package app

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

type memReviews struct {
	dirs  []string // the models, in queue order
	marks map[string]string
}

func (m *memReviews) open(creator string) []ReviewCandidate {
	var out []ReviewCandidate
	for i, d := range m.dirs {
		if _, done := m.marks[d]; !done {
			out = append(out, ReviewCandidate{ID: int64(i + 1), Dir: d})
		}
	}
	return out
}
func (m *memReviews) ReviewCandidates(_ context.Context, creator string, skip, limit int) ([]ReviewCandidate, error) {
	all := m.open(creator)
	if skip > len(all) {
		skip = len(all)
	}
	all = all[skip:]
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
func (m *memReviews) ReviewCounts(context.Context, string) (int, int, error) {
	return len(m.open("")), len(m.marks), nil
}
func (m *memReviews) MarkReview(_ context.Context, dir, action string, _ int64) error {
	m.marks[dir] = action
	return nil
}
func (m *memReviews) UnmarkReview(_ context.Context, dir string) error {
	delete(m.marks, dir)
	return nil
}
func (m *memReviews) ResetReviews(context.Context, string) (int, error) {
	n := 0
	for d, a := range m.marks {
		if a == ReviewSkip {
			delete(m.marks, d)
			n++
		}
	}
	return n, nil
}

type overrideSet map[string]bool

func (o overrideSet) Get(string) ([]byte, int64, bool, error) { return nil, 0, false, nil }
func (o overrideSet) Put(string, []byte) error                { return nil }
func (o overrideSet) Delete(string) error                     { return nil }
func (o overrideSet) Stat(key string) (int64, bool)           { return 5, o[key] }

type reviewModels struct{ Store }

func (reviewModels) Model(_ context.Context, id int64) (*ModelDetail, error) {
	m := ModelDetail{}
	m.ID = id
	m.Dir = map[int64]string{1: "C/A", 2: "C/B", 3: "C/C", 4: "C/D"}[id]
	return &m, nil
}

func TestReviewSkipsModelsWithAChosenPictureAndDoesNotRepeatReviewedOnes(t *testing.T) {
	mem := &memReviews{dirs: []string{"C/A", "C/B", "C/C", "C/D"}, marks: map[string]string{}}
	r := &PreviewReview{Store: reviewModels{}, Reviews: mem, Overrides: overrideSet{overrideKey("C/B"): true}}
	ctx := context.Background()
	st, err := r.State(ctx, "", 3)
	if err != nil || !reflect.DeepEqual(st.Next, []int64{1, 3, 4}) || st.Remaining != 3 || st.Done != 1 {
		t.Fatalf("%+v %v", st, err) // B had a picture chosen before: counted as done
	}
	if mem.marks["C/B"] != ReviewSet {
		t.Errorf("marks %v", mem.marks)
	}
	if err := r.Skip(ctx, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = r.State(ctx, "", 3)
	if !reflect.DeepEqual(st.Next, []int64{3, 4}) || st.Remaining != 2 {
		t.Errorf("after skip: %+v", st)
	}
	if err := r.Undo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = r.State(ctx, "", 1)
	if !reflect.DeepEqual(st.Next, []int64{1}) {
		t.Errorf("after undo: %+v", st)
	}
	r.Skip(ctx, 3)
	n, _ := r.Reset(ctx, "")
	var left []string
	for d, a := range mem.marks {
		left = append(left, d+":"+a)
	}
	sort.Strings(left)
	if n != 1 || !reflect.DeepEqual(left, []string{"C/B:set"}) {
		t.Errorf("reset %d %v", n, left)
	}
}
