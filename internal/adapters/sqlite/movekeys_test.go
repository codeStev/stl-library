package sqlite

import (
	"context"
	"testing"
)

func TestMoveKeysCarriesUserDataAlongWithAMovedModel(t *testing.T) {
	s := open(t)
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	from := "Loot Studios/Abyssal Haze/Enemies/Bell Head"
	to := "Loot Studios/Winter/Bell Head"
	var id int64
	var first int64
	if err := s.db.QueryRow(`SELECT id, first_seen FROM model WHERE dir = ?`, from).Scan(&id, &first); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tag (dir, tag) VALUES (?, 'boss')`, from)
	exec(`INSERT INTO print (dir, variant_dir, printed_unix, note) VALUES (?, ?, 1, '')`, from, from+"/32mm/Supported")
	exec(`INSERT INTO print_job (id, name, created_unix) VALUES (1, 'j', 1)`)
	exec(`INSERT INTO job_item (job_id, part_path, count) VALUES (1, ?, 2)`, from+"/32mm/Supported/bell.stl")
	exec(`INSERT INTO tag (dir, tag) VALUES ('Loot Studios/Abyssal Haze/Enemies/Bell Headless', 'other')`)
	if err := s.MoveKeys(context.Background(), from, to); err != nil {
		t.Fatal(err)
	}
	var dir string
	var gotID, gotFirst int64
	if err := s.db.QueryRow(`SELECT dir, id, first_seen FROM model WHERE id = ?`, id).Scan(&dir, &gotID, &gotFirst); err != nil || dir != to || gotFirst != first {
		t.Fatalf("model: %q %d %v", dir, gotFirst, err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM tag WHERE dir = '` + to + `'`:                                                 1,
		`SELECT count(*) FROM tag WHERE dir = '` + from + `'`:                                               0,
		`SELECT count(*) FROM tag WHERE dir = 'Loot Studios/Abyssal Haze/Enemies/Bell Headless'`:            1, // a sibling with the same prefix stays
		`SELECT count(*) FROM print WHERE dir = '` + to + `' AND variant_dir = '` + to + `/32mm/Supported'`: 1,
		`SELECT count(*) FROM job_item WHERE part_path = '` + to + `/32mm/Supported/bell.stl'`:              1,
		`SELECT count(*) FROM part WHERE path = '` + to + `/32mm/Supported/bell.stl'`:                       1,
		`SELECT count(*) FROM variant WHERE dir = '` + to + `/32mm/No Supports'`:                            1,
		`SELECT count(*) FROM image WHERE path = '` + to + `/cover.jpg'`:                                    1,
	} {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d (%v), want %d", q, n, err, want)
		}
	}
}
