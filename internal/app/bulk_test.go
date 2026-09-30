package app

import (
	"context"
	"errors"
	"strings"

	"github.com/codeStev/stl-library/internal/core/library"
	"testing"
)

type bulkStore struct {
	Store
	models map[int64]ModelDetail
	fixes  [][2]string
}

func (s *bulkStore) Model(_ context.Context, id int64) (*ModelDetail, error) {
	m, ok := s.models[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &m, nil
}
func (s *bulkStore) AddFix(_ context.Context, from, to string, _ int64) (int64, error) {
	s.fixes = append(s.fixes, [2]string{from, to})
	return int64(len(s.fixes)), nil
}

type fakeEditor struct {
	dirs  map[string]bool
	moves [][2]string
}

func (e *fakeEditor) Stat(_ context.Context, rel string) (int64, bool, error) {
	return 0, e.dirs[rel], nil
}
func (e *fakeEditor) Rename(ctx context.Context, from, to string) error { return e.Move(ctx, from, to) }
func (e *fakeEditor) Move(_ context.Context, from, to string) error {
	if e.dirs[to] {
		return errors.New("exists")
	}
	delete(e.dirs, from)
	e.dirs[to] = true
	e.moves = append(e.moves, [2]string{from, to})
	return nil
}

type fakeKeys struct{ moved [][2]string }

func (k *fakeKeys) MoveKeys(_ context.Context, from, to string) error {
	k.moved = append(k.moved, [2]string{from, to})
	return nil
}

type fakeModelDirs map[string]bool

func (d fakeModelDirs) IsModelDir(_ context.Context, dir string) (bool, error) { return d[dir], nil }

func md(id int64, creator, release, category, name string) ModelDetail {
	m := ModelDetail{}
	m.ID, m.Creator, m.Release, m.Category, m.Name = id, creator, release, category, name
	m.Dir = joinLevels(creator, release, category, name)
	return m
}

func sp(s string) *string { return &s }

func newBulk() (Bulk, *bulkStore, *fakeEditor, *fakeKeys) {
	st := &bulkStore{models: map[int64]ModelDetail{
		1: md(1, "Unbekannt", "", "", "Mercy 2"),
		2: md(2, "Unbekannt", "", "", "Mercy"),
		3: md(3, "Loot", "Winter", "Heroes", "Bell"),
		4: md(4, "Loot", "Winter", "Heroes", "Bell Head"),
	}}
	ed := &fakeEditor{dirs: map[string]bool{"Unbekannt/Mercy 2": true, "Unbekannt/Mercy": true, "Loot/Winter/Heroes/Bell": true, "Loot/Winter/Heroes/Bell Head": true}}
	keys := &fakeKeys{}
	return Bulk{Store: st, Editor: ed, Keys: keys, Dirs: fakeModelDirs{}}, st, ed, keys
}

func TestBulkMovesToAnotherCreatorAndReleaseAndJournalsEachMove(t *testing.T) {
	b, st, ed, keys := newBulk()
	res, err := b.Apply(context.Background(), BulkEdit{IDs: []int64{3, 4}, Creator: sp("Loot Studios"), Release: sp("Winter 2022"), Category: sp("")})
	if err != nil || res.Done != 2 || len(res.Failed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !ed.dirs["Loot Studios/Winter 2022/Bell"] || !ed.dirs["Loot Studios/Winter 2022/Bell Head"] || ed.dirs["Loot/Winter/Heroes/Bell"] {
		t.Errorf("%v", ed.dirs)
	}
	if len(keys.moved) != 2 || len(st.fixes) != 2 || st.fixes[0][1] != "Loot Studios/Winter 2022/Bell" {
		t.Errorf("keys %v fixes %v", keys.moved, st.fixes)
	}
}

func TestBulkFindReplaceRenamesAndLeavesProblemsAlone(t *testing.T) {
	b, _, ed, _ := newBulk()
	rows, err := b.Plan(context.Background(), BulkEdit{IDs: []int64{1, 2}, Find: " 2", Replace: ""})
	if err != nil {
		t.Fatal(err)
	}
	// "Mercy 2" -> "Mercy" exists; "Mercy" does not change
	if rows[0].Problem != "a folder of that name exists already" || rows[1].Problem != "nothing changes" {
		t.Errorf("%+v", rows)
	}
	res, err := b.Apply(context.Background(), BulkEdit{IDs: []int64{1, 2}, Find: " 2", Replace: ""})
	if err != nil || res.Done != 0 || len(ed.moves) != 0 {
		t.Errorf("%+v %v %v", res, err, ed.moves)
	}
}

func TestBulkRefusesTwoModelsOnOneTargetAndTargetsInsideModels(t *testing.T) {
	b, _, _, _ := newBulk()
	rows, _ := b.Plan(context.Background(), BulkEdit{IDs: []int64{3, 4}, Find: "Bell Head", Replace: "Bell", Category: sp("")})
	if rows[0].Problem != "" || rows[1].Problem != "same target as Loot/Winter/Heroes/Bell" {
		t.Errorf("%+v", rows)
	}
	b.Dirs = fakeModelDirs{"Unbekannt": true}
	rows, _ = b.Plan(context.Background(), BulkEdit{IDs: []int64{1}, Release: sp("Inner")})
	if !strings.Contains(rows[0].Problem, "inside another model") {
		t.Errorf("%+v", rows)
	}
}

func TestBulkRejectsBadNamesAndEmptyEdits(t *testing.T) {
	b, _, _, _ := newBulk()
	for _, e := range []BulkEdit{
		{IDs: []int64{1}},
		{IDs: nil, Creator: sp("X")},
		{IDs: []int64{1}, Creator: sp("")},
		{IDs: []int64{1}, Creator: sp("a/b")},
		{IDs: []int64{1}, Creator: sp("_hidden")},
		{IDs: []int64{1}, Release: sp("..")},
		{IDs: []int64{1}, Release: sp("a:b")},
	} {
		if _, err := b.Plan(context.Background(), e); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v", e)
		}
	}
	rows, _ := b.Plan(context.Background(), BulkEdit{IDs: []int64{1}, Find: "Mercy", Replace: "a/b"})
	if rows[0].Problem == "" {
		t.Error("a name with a slash was planned")
	}
	rows, _ = b.Plan(context.Background(), BulkEdit{IDs: []int64{1}, Find: "Mercy 2", Replace: ""})
	if rows[0].Problem == "" {
		t.Error("an empty name was planned")
	}
}

type issueStore struct {
	bulkStore
	issues []library.Issue
}

func (s *issueStore) Issues(context.Context) ([]library.Issue, error) { return s.issues, nil }

func TestFixSplitsAFolderIntoCanonicalLevelsAndCarriesItsData(t *testing.T) {
	st := &issueStore{issues: []library.Issue{{Dir: "C/R/M/Supported_32mm"}}}
	ed := &fakeEditor{dirs: map[string]bool{"C/R/M/Supported_32mm": true}}
	keys := &fakeKeys{}
	fx := Fixes{Store: st, Editor: ed, Keys: keys}
	ctx := context.Background()
	sug, err := fx.Suggestions(ctx)
	if err != nil || len(sug) != 1 || sug[0].To != "C/R/M/32mm/Supported" {
		t.Fatalf("%+v %v", sug, err)
	}
	if _, err := fx.Apply(ctx, "C/R/M/Supported_32mm", "C/R/Other/32mm/Supported"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a target outside the parent: %v", err)
	}
	if _, err := fx.Apply(ctx, "C/R/M/Supported_32mm", "C/R/M/32mm/Supported"); err != nil {
		t.Fatal(err)
	}
	if !ed.dirs["C/R/M/32mm/Supported"] || len(keys.moved) != 1 || len(st.fixes) != 1 {
		t.Errorf("%v %v %v", ed.dirs, keys.moved, st.fixes)
	}
}

func TestBulkFindReplaceTrimsTheEdgesOfTheNewName(t *testing.T) {
	b, _, _, _ := newBulk()
	rows, _ := b.Plan(context.Background(), BulkEdit{IDs: []int64{4}, Find: "Head", Replace: ""})
	if rows[0].To != "Loot/Winter/Heroes/Bell" && rows[0].Problem == "" {
		t.Errorf("%+v", rows[0])
	}
	if rows[0].Problem != "a folder of that name exists already" {
		t.Errorf("%+v", rows[0])
	}
}
