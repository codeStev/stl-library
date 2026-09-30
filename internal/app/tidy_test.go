package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeTidier struct {
	items   []TidyItem
	removed []string
	fail    map[string]bool
}

func (f *fakeTidier) Find(context.Context, func(int)) ([]TidyItem, error) { return f.items, nil }
func (f *fakeTidier) Remove(_ context.Context, it TidyItem) error {
	if f.fail[it.Path] {
		return errors.New("nope")
	}
	f.removed = append(f.removed, it.Path)
	return nil
}

func waitTidy(t *testing.T, td *Tidy) {
	t.Helper()
	for i := 0; i < 200 && td.State().Running; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if td.State().Running {
		t.Fatal("still running")
	}
}

func TestTidyAppliesFilesBeforeFoldersDeepestFirstAndOnlyKnownItems(t *testing.T) {
	ft := &fakeTidier{items: []TidyItem{
		{Kind: TidyEmptyFolder, Path: "C/E"},
		{Kind: TidyEmptyFolder, Path: "C/E/Sub"},
		{Kind: TidyJunkFile, Path: "C/E/Sub/.DS_Store"},
		{Kind: TidyCopy, Path: "C/M/a (2).stl", Original: "C/M/a.stl"},
	}, fail: map[string]bool{"C/M/a (2).stl": true}}
	td := &Tidy{Tidier: ft}
	if !td.Start(context.Background()) {
		t.Fatal("not started")
	}
	waitTidy(t, td)
	res, err := td.Apply(context.Background(), []string{"C/E", "C/E/Sub", "C/E/Sub/.DS_Store", "C/M/a (2).stl"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"C/E/Sub/.DS_Store", "C/E/Sub", "C/E"}; !reflect.DeepEqual(ft.removed, want) {
		t.Errorf("order %v want %v", ft.removed, want)
	}
	if res.Done != 3 || len(res.Failed) != 1 {
		t.Errorf("%+v", res)
	}
	if left := td.State().Items; len(left) != 1 || left[0].Path != "C/M/a (2).stl" {
		t.Errorf("left %v", left)
	}
	if _, err := td.Apply(context.Background(), []string{"C/Other"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown path accepted: %v", err)
	}
}
