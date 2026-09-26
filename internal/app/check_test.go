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
