package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/codeStev/stl-library/convention"
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

func TestModelDetailVariantsImagesCreatorsIssues(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search(ctx, app.Query{Text: "bell", Limit: 1})
	m, err := s.Model(ctx, hits[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Bell Head" || m.Category != "Enemies" || len(m.Variants) != 2 || len(m.Images) != 1 || m.Cover != m.Images[0].ID {
		t.Fatalf("model: %+v", m)
	}
	v := m.Variants[0] // ordered by dir: "32mm/No Supports" before "32mm/Supported"
	if v.Dims.Scale != "32mm" || v.Dims.Supports != "No Supports" || len(v.Parts) != 1 || v.Parts[0].Size != 10 || v.ModelID != m.ID {
		t.Errorf("variant: %+v", v)
	}
	img, err := s.Image(ctx, m.Images[0].ID)
	if err != nil || img.Path != "Loot Studios/Abyssal Haze/Enemies/Bell Head/cover.jpg" {
		t.Errorf("image: %+v %v", img, err)
	}
	if p, err := s.Part(ctx, v.Parts[0].ID); err != nil || p.Path != "Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl" {
		t.Errorf("part: %+v %v", p, err)
	}
	for _, f := range []func() error{
		func() error { _, err := s.Part(ctx, 9999); return err },
		func() error { _, err := s.Model(ctx, 9999); return err },
		func() error { _, err := s.Variant(ctx, 9999); return err },
		func() error { _, err := s.Image(ctx, 9999); return err },
	} {
		if err := f(); err != app.ErrNotFound {
			t.Errorf("unknown id: err = %v", err)
		}
	}
	cs, _ := s.Creators(ctx)
	if len(cs) != 2 || cs[0].Name != "Artisan Guild" || cs[1].Models != 2 {
		t.Errorf("creators: %+v", cs)
	}
	is, _ := s.Issues(ctx)
	if len(is) != 1 || is[0].Dir != "Lord of the Print/Unchained/Araki/Presupported" {
		t.Errorf("issues: %+v", is)
	}
	page2, _ := s.Search(ctx, app.Query{Creator: "Loot Studios", Limit: 1, Offset: 1})
	if len(page2) != 1 || page2[0].Name != "Élise the Brave" {
		t.Errorf("offset: %+v", page2)
	}
}

func TestUserDataIsSearchableAndSurvivesRescansAndAbsence(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	find := func(q app.Query) []app.ModelSummary {
		t.Helper()
		q.Limit = 10
		hits, err := s.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	bell := find(app.Query{Text: "bell"})[0]
	if err := s.SetTags(ctx, bell.ID, []string{"Painted", "Dragon Slayer"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisplayName(ctx, bell.ID, "Bellringer of Doom"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []app.Query{{Text: "doom"}, {Text: "slayer"}, {Tag: "painted"}} {
		hits := find(q)
		if len(hits) != 1 || hits[0].ID != bell.ID || hits[0].DisplayName != "Bellringer of Doom" || len(hits[0].Tags) != 2 {
			t.Errorf("%+v: %+v", q, hits)
		}
	}
	// A changed model (rewritten in place) and a rescan keep the user data.
	changed := append(append([]string{}, libraryV1...), "Loot Studios/Abyssal Haze/Enemies/Bell Head/75mm/Supported/b.stl")
	if _, err := sync(s, changed...); err != nil {
		t.Fatal(err)
	}
	if hits := find(app.Query{Text: "doom"}); len(hits) != 1 {
		t.Errorf("after update: %+v", hits)
	}
	// Gone for one scan, back in the next: the data is still attached.
	if _, err := sync(s, libraryV1[3:]...); err != nil {
		t.Fatal(err)
	}
	if tags, _ := s.Tags(ctx); len(tags) != 0 {
		t.Errorf("tags of absent models listed: %+v", tags)
	}
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	back := find(app.Query{Tag: "Dragon Slayer"})
	if len(back) != 1 || back[0].DisplayName != "Bellringer of Doom" {
		t.Errorf("after coming back: %+v", back)
	}
	if tags, _ := s.Tags(ctx); len(tags) != 2 || tags[0].Tag != "Dragon Slayer" || tags[0].Models != 1 {
		t.Errorf("tags: %+v", tags)
	}
	// Clearing the display name goes back to the folder name.
	s.SetDisplayName(ctx, back[0].ID, "")
	if hits := find(app.Query{Text: "bell"}); hits[0].DisplayName != "" {
		t.Errorf("display name not cleared: %+v", hits[0])
	}
	if err := s.SetTags(ctx, 9999, nil); err != app.ErrNotFound {
		t.Errorf("unknown model: %v", err)
	}
}

func TestPrintsAndQueue(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search(ctx, app.Query{Text: "bell", Limit: 1})
	m, _ := s.Model(ctx, hits[0].ID)
	v1, v2 := m.Variants[0], m.Variants[1]

	if err := s.Enqueue(ctx, v2.ID, 100, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, v1.ID, 200, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, v2.ID, 300, "updated note"); err != nil { // keeps its place
		t.Fatal(err)
	}
	q, _ := s.Queue(ctx)
	if len(q) != 2 || q[0].VariantID != v2.ID || q[0].Note != "updated note" || q[0].Label != "32mm Supported" || q[0].Model != "Bell Head" {
		t.Fatalf("queue: %+v", q)
	}

	p, err := s.AddPrint(ctx, v2.ID, 400, "grey primer")
	if err != nil {
		t.Fatal(err)
	}
	printed, never := true, false
	if hits, _ := s.Search(ctx, app.Query{Printed: &printed, Limit: 10}); len(hits) != 1 || hits[0].Prints != 1 {
		t.Errorf("printed filter: %+v", hits)
	}
	if hits, _ := s.Search(ctx, app.Query{Printed: &never, Limit: 10}); len(hits) != 2 {
		t.Errorf("never printed: %+v", hits)
	}
	v, _ := s.Variant(ctx, v2.ID)
	if len(v.Prints) != 1 || v.Prints[0].Note != "grey primer" || !v.Queued {
		t.Errorf("variant: %+v", v)
	}
	if err := s.Dequeue(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Dequeue(ctx, v2.ID); err != app.ErrNotFound {
		t.Errorf("dequeue twice: %v", err)
	}
	if err := s.DeletePrint(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePrint(ctx, p.ID); err != app.ErrNotFound {
		t.Errorf("delete twice: %v", err)
	}
	if _, err := s.AddPrint(ctx, 9999, 1, ""); err != app.ErrNotFound {
		t.Errorf("unknown variant: %v", err)
	}
}

func TestMigratingAVersion1IndexKeepsItsModelsSearchable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + `; PRAGMA user_version = 1;
		INSERT INTO model (dir, creator, release, category, name, variants, parts, bytes, sig) VALUES ('C/R/Old One', 'C', 'R', '', 'Old One', 0, 0, 0, 'x');
		INSERT INTO model_fts (rowid, name, creator, release, category) VALUES (1, 'Old One', 'C', 'R', '')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if hits, err := s.Search(context.Background(), app.Query{Text: "old", Limit: 5}); err != nil || len(hits) != 1 {
		t.Errorf("after migration: %v %v", hits, err)
	}
}

func TestImportLog(t *testing.T) {
	s, ctx := open(t), context.Background()
	if ok, _ := s.ImportBaselined(ctx); ok {
		t.Error("baselined before the first run")
	}
	s.SetImportBaselined(ctx)
	s.SetImportBaselined(ctx)
	if ok, _ := s.ImportBaselined(ctx); !ok {
		t.Error("not baselined")
	}
	r := app.ImportRecord{Source: "wicked/Panther", Signature: "a", State: app.ImportWaiting, Message: "wait", UpdatedUnix: 1}
	s.SaveImport(ctx, r)
	r.State, r.Target, r.Files, r.UpdatedUnix = app.ImportDone, "Wicked/Panther", 3, 2
	s.SaveImport(ctx, r)
	s.SaveImport(ctx, app.ImportRecord{Source: "nomnom/Old", State: app.ImportExisting, UpdatedUnix: 1})
	recs, err := s.ImportRecords(ctx)
	if err != nil || len(recs) != 2 || recs[0].Source != "wicked/Panther" || recs[0].Files != 3 || recs[0].Target != "Wicked/Panther" {
		t.Errorf("records: %+v %v", recs, err)
	}
}

func TestHiddenModelsAndVariantLabels(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := sync(s, libraryV1...); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search(ctx, app.Query{Text: "bell", Limit: 1})
	bell := hits[0]
	if err := s.SetHidden(ctx, bell.ID, true); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, app.Query{Text: "bell", Limit: 5}); len(hits) != 0 {
		t.Errorf("hidden model found: %+v", hits)
	}
	if hits, _ := s.Search(ctx, app.Query{Text: "bell", Hidden: true, Limit: 5}); len(hits) != 1 || !hits[0].Hidden {
		t.Errorf("with hidden: %+v", hits)
	}
	if _, err := sync(s, libraryV1...); err != nil { // survives a rescan
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, app.Query{Creator: "Loot Studios", Limit: 5}); len(hits) != 1 {
		t.Errorf("after rescan: %+v", hits)
	}
	s.SetHidden(ctx, bell.ID, false)

	m, _ := s.Model(ctx, bell.ID)
	v := m.Variants[0] // 32mm No Supports
	label := &app.VariantLabel{Dims: convention.Dims{Scale: "75mm", Supports: "Supported", Fill: "Hollow"}, Option: "Helmet"}
	if err := s.SetVariantLabel(ctx, v.ID, label); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Variant(ctx, v.ID)
	if got.Dims != label.Dims || got.Option != "Helmet" || !got.Relabeled {
		t.Errorf("relabeled: %+v", got)
	}
	s.Enqueue(ctx, v.ID, 1, "")
	if q, _ := s.Queue(ctx); len(q) != 1 || q[0].Label != "75mm Supported Hollow Helmet" {
		t.Errorf("queue label: %+v", q)
	}
	s.SetVariantLabel(ctx, v.ID, nil)
	if got, _ := s.Variant(ctx, v.ID); got.Dims.Scale != "32mm" || got.Relabeled {
		t.Errorf("reset: %+v", got)
	}
}

func TestPrinterSettings(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, ok, err := s.PrinterSettings(ctx); ok || err != nil {
		t.Errorf("before saving: %v %v", ok, err)
	}
	want := app.PrinterSettings{Host: "192.168.2.40", ControlPort: 3030}
	s.SavePrinterSettings(ctx, want)
	s.SavePrinterSettings(ctx, want)
	if got, ok, err := s.PrinterSettings(ctx); !ok || err != nil || got != want {
		t.Errorf("got %+v %v %v", got, ok, err)
	}
}

func TestNotificationSettingsAndPartVariant(t *testing.T) {
	s, ctx := open(t), context.Background()
	ns, err := s.NotificationSettings(ctx)
	if err != nil || ns.Events == nil || ns.Ntfy.URL != "" {
		t.Errorf("defaults: %+v %v", ns, err)
	}
	s.SaveNotificationSettings(ctx, app.NotificationSettings{Ntfy: app.NtfySettings{URL: "https://ntfy.sh/x", Token: "v1:abc"}, Events: map[string]bool{app.EventPrintDone: true}})
	if ns, _ = s.NotificationSettings(ctx); ns.Ntfy.Token != "v1:abc" || !ns.Events[app.EventPrintDone] {
		t.Errorf("saved: %+v", ns)
	}
	sync(s, libraryV1...)
	hits, _ := s.Search(ctx, app.Query{Text: "bell", Limit: 1})
	m, _ := s.Model(ctx, hits[0].ID)
	if v, err := s.PartVariant(ctx, m.Variants[0].Parts[0].ID); err != nil || v != m.Variants[0].ID {
		t.Errorf("part variant: %d %v", v, err)
	}
}
