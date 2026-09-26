package app

import (
	"context"
	"errors"
	"testing"

	"github.com/codeStev/stl-library/internal/core/library"
)

type fakeLister struct {
	files []library.File
	err   error
}

func (f fakeLister) List(context.Context) ([]library.File, error) { return f.files, f.err }

func TestCheckReportsModelsAndIssues(t *testing.T) {
	r, err := Check(context.Background(), fakeLister{files: []library.File{
		{Path: "Loot Studios/Rel/Model/32mm/Supported/a.stl"},
		{Path: "Loot Studios/Rel/Other/Presupported/b.stl"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Files != 2 || len(r.Models) != 1 || len(r.Issues) != 1 {
		t.Errorf("files=%d models=%d issues=%v", r.Files, len(r.Models), r.Issues)
	}
}

func TestCheckPassesListingErrorsOn(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Check(context.Background(), fakeLister{err: boom}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

type fakeStore struct {
	synced []*library.Model
	query  Query
}

func (f *fakeStore) Sync(_ context.Context, m []*library.Model, i []library.Issue) (SyncStats, error) {
	f.synced = m
	return SyncStats{Added: len(m), Issues: len(i)}, nil
}

func (f *fakeStore) Search(_ context.Context, q Query) ([]ModelSummary, error) {
	f.query = q
	return nil, nil
}

func TestScanSyncsWhatWasRead(t *testing.T) {
	s := &fakeStore{}
	st, err := Scan(context.Background(), fakeLister{files: []library.File{
		{Path: "C/R/M/Supported/a.stl"}, {Path: "C/R/N/n.stl"},
	}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.Added != 2 || len(s.synced) != 2 {
		t.Errorf("stats %+v, synced %d", st, len(s.synced))
	}
}

func TestSearchDefaultsTheLimit(t *testing.T) {
	s := &fakeStore{}
	if _, err := Search(context.Background(), s, Query{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if s.query.Limit != 50 {
		t.Errorf("limit %d", s.query.Limit)
	}
}
