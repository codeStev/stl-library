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
