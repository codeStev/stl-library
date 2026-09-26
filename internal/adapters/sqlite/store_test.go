package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/library"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func read(paths ...string) ([]*library.Model, []library.Issue) {
	var fs []library.File
	for _, p := range paths {
		fs = append(fs, library.File{Path: p, Size: 10, ModUnix: 1})
	}
	return library.Read(fs)
}

var libraryV1 = []string{
	"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported/bell.stl",
	"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl",
	"Loot Studios/Abyssal Haze/Enemies/Bell Head/cover.jpg",
	"Loot Studios/Abyssal Haze/Heroes/Élise the Brave/32mm/Supported/e.stl",
	"Artisan Guild/Noble Alfar/Goldhorn Cervid Rider/Supported/r.stl",
	"Lord of the Print/Unchained/Araki/Presupported/a.stl",
}

func sync(s *Store, paths ...string) (app.SyncStats, error) {
	models, issues := read(paths...)
	return s.Sync(context.Background(), models, issues)
}

func changes(t *testing.T, s *Store) int {
	var n int
	if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func count(t *testing.T, s *Store, table string) int {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncAddsThenARescanWithoutChangesWritesNothing(t *testing.T) {
	s := open(t)
	st, err := sync(s, libraryV1...)
	if err != nil {
		t.Fatal(err)
	}
	if st.Added != 3 || st.Issues != 1 {
		t.Errorf("first sync: %+v", st)
	}
	if count(t, s, "variant") != 4 || count(t, s, "part") != 4 || count(t, s, "image") != 1 {
		t.Errorf("rows: variants %d parts %d images %d", count(t, s, "variant"), count(t, s, "part"), count(t, s, "image"))
	}
	before := changes(t, s)
	st, err = sync(s, libraryV1...)
	if err != nil {
		t.Fatal(err)
	}
	if st.Unchanged != 3 || st.Added+st.Updated+st.Removed != 0 {
		t.Errorf("rescan: %+v", st)
	}
	if d := changes(t, s) - before; d != 0 {
		t.Errorf("rescan without changes wrote %d rows", d)
	}
}

func TestChangedModelKeepsItsIDAndRemovedModelsTakeTheirRowsAlong(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	id := func(name string) int64 {
		hits, err := s.Search(ctx, app.Query{Text: name, Limit: 5})
		if err != nil || len(hits) != 1 {
			t.Fatalf("search %q: %v %v", name, hits, err)
		}
		return hits[0].ID
	}
	bell := id("bell head")

	v2 := append([]string{}, libraryV1[:3]...)
	v2 = append(v2, "Loot Studios/Abyssal Haze/Enemies/Bell Head/75mm/Supported/bell75.stl")
	st, err := sync(s, v2...)
	if err != nil {
		t.Fatal(err)
	}
	if st.Updated != 1 || st.Removed != 2 || st.Issues != 0 {
		t.Errorf("sync v2: %+v", st)
	}
	if got := id("bell"); got != bell {
		t.Errorf("Bell Head changed id %d -> %d", bell, got)
	}
	if count(t, s, "model") != 1 || count(t, s, "variant") != 3 || count(t, s, "part") != 3 || count(t, s, "issue") != 0 {
		t.Errorf("leftover rows: models %d variants %d parts %d issues %d",
			count(t, s, "model"), count(t, s, "variant"), count(t, s, "part"), count(t, s, "issue"))
	}
	if hits, _ := s.Search(ctx, app.Query{Text: "elise", Limit: 5}); len(hits) != 0 {
		t.Errorf("removed model still found: %v", hits)
	}
}

func TestSearchMatchesWordPrefixesAcrossFieldsAndFiltersByCreator(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q    app.Query
		want []string
	}{
		{app.Query{Text: "abys bell"}, []string{"Bell Head"}},
		{app.Query{Text: "elise"}, []string{"Élise the Brave"}}, // diacritics folded
		{app.Query{Text: "gold"}, []string{"Goldhorn Cervid Rider"}},
		{app.Query{Text: `bell" OR name:*`}, nil}, // FTS syntax is not interpreted: all three words must match
		{app.Query{Creator: "Loot Studios"}, []string{"Bell Head", "Élise the Brave"}},
		{app.Query{Text: "nothing-like-this"}, nil},
	} {
		c.q.Limit = 10
		hits, err := s.Search(ctx, c.q)
		if err != nil {
			t.Fatalf("%+v: %v", c.q, err)
		}
		var names []string
		for _, h := range hits {
			names = append(names, h.Name)
		}
		if len(names) != len(c.want) {
			t.Errorf("%+v: got %v, want %v", c.q, names, c.want)
			continue
		}
		for i := range names {
			if names[i] != c.want[i] {
				t.Errorf("%+v: got %v, want %v", c.q, names, c.want)
				break
			}
		}
	}
}

func TestReopeningKeepsTheSchemaAndData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count(t, s, "model") != 3 {
		t.Errorf("models after reopen: %d", count(t, s, "model"))
	}
}
