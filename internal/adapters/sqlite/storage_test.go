package sqlite

import (
	"context"
	"testing"
)

func TestStorageReportsSpaceByCreatorKindAndDuplicates(t *testing.T) {
	s := open(t)
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	// the two "bell" files are the same content
	for _, p := range []string{"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported/bell.stl", "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl"} {
		if _, err := s.db.Exec(`INSERT INTO file_hash (path, size, mod_unix, sha256, hashed_unix, verified_unix) VALUES (?, 10, 1, 'abc', 1, 1)`, p); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Storage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Models != 3 || r.Bytes <= 0 {
		t.Errorf("models %d bytes %d", r.Models, r.Bytes)
	}
	if len(r.Creators) != 2 || r.Creators[0].Name != "Loot Studios" || len(r.Creators[0].Releases) != 1 {
		t.Errorf("creators %+v", r.Creators)
	}
	if len(r.Largest) != 3 || r.Largest[0].Bytes < r.Largest[2].Bytes {
		t.Errorf("largest %+v", r.Largest)
	}
	kinds := map[string]int{}
	for _, k := range r.Kinds {
		kinds[k.Ext] = k.Files
	}
	if kinds["stl"] != 4 || kinds["jpg"] != 1 {
		t.Errorf("kinds %v", kinds)
	}
	if r.DuplicateGroups != 1 || r.DuplicateBytes != 10 || r.Hashed != 2 || r.Total != 4 {
		t.Errorf("duplicates %+v", r)
	}
}

func TestMergedDuplicatesCountAsOneCopyWhileTheyStayUnchanged(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	a, b := "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported/bell.stl", "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl"
	for _, p := range []string{a, b} {
		if _, err := s.db.Exec(`INSERT INTO file_hash (path, size, mod_unix, sha256, hashed_unix, verified_unix) VALUES (?, 10, 1, 'abc', 1, 1)`, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, total, err := s.DuplicateGroups(ctx, 0, 10, 0); err != nil || total != 1 {
		t.Fatalf("before: %d %v", total, err)
	}
	if err := s.SaveMerge(ctx, b, 10, 1, 5); err != nil {
		t.Fatal(err)
	}
	groups, total, err := s.DuplicateGroups(ctx, 0, 10, 0)
	if err != nil || total != 0 || len(groups) != 0 {
		t.Fatalf("merged: %d %v %v", total, groups, err)
	}
	g, err := s.DuplicateGroup(ctx, "abc", 10)
	if err != nil || len(g.Files) != 2 || g.Wasted() != 0 {
		t.Fatalf("group: %+v %v", g, err)
	}
	if r, _ := s.Storage(ctx); r.DuplicateGroups != 0 || r.DuplicateBytes != 0 {
		t.Errorf("storage: %+v", r)
	}
	// The file changed since: the record no longer counts.
	if _, err := s.db.Exec(`UPDATE file_hash SET mod_unix = 2 WHERE path = ?`, b); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.DuplicateGroups(ctx, 0, 10, 0); total != 1 {
		t.Errorf("after change: %d", total)
	}
}

func TestSetVariantsListsTheModelFilesOfModelsWithSeveralVariants(t *testing.T) {
	s := open(t)
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	vs, err := s.SetVariants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Key != vs[1].Key || vs[0].Supports == vs[1].Supports || len(vs[0].Files) != 1 || len(vs[1].Files) != 1 {
		t.Errorf("variants %+v", vs)
	}
}

func TestCorruptEventsSayWhereTheGoodContentIsAndMetaKeepsValues(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	a, b := "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported/bell.stl", "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl"
	for _, p := range []string{a, b} {
		if _, err := s.db.Exec(`INSERT INTO file_hash (path, size, mod_unix, sha256, hashed_unix, verified_unix) VALUES (?, 10, 1, 'abc', 1, 1)`, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO health_event (kind, path, detail, at_unix) VALUES ('corrupt', ?, 'x', 1)`, a); err != nil {
		t.Fatal(err)
	}
	es, err := s.HealthEvents(ctx, "corrupt")
	if err != nil || len(es) != 1 || len(es[0].OtherCopies) != 1 || es[0].OtherCopies[0] != b {
		t.Fatalf("events %+v %v", es, err)
	}
	if _, ok, _ := s.Meta(ctx, "k"); ok {
		t.Error("unset key found")
	}
	if err := s.SetMeta(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Meta(ctx, "k"); !ok || v != "v" {
		t.Errorf("meta %q %v", v, ok)
	}
}
