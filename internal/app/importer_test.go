package app

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codeStev/stl-library/internal/core/importer"
)

type fakeDownloads struct {
	files    []importer.File     // plain files, Rel from the root
	archives map[string][]string // archive Rel from root -> entry paths
	broken   map[string]bool     // archives that can't be read yet
}

func (d *fakeDownloads) List(context.Context) ([]importer.File, error) { return d.files, nil }

func (d *fakeDownloads) Expand(_ context.Context, unit string, files []importer.File) ([]importer.File, error) {
	var out []importer.File
	for _, f := range files {
		full := unit + "/" + f.Rel
		if !importer.IsArchive(f.Rel) {
			out = append(out, f)
			continue
		}
		if d.broken[full] {
			return nil, errors.New("zip: not a valid zip file")
		}
		base := strings.TrimSuffix(f.Rel, ".zip")
		for _, e := range d.archives[full] {
			out = append(out, importer.File{Rel: base + "/" + e, Archive: f.Rel, Entry: e, Size: 1, ModUnix: f.ModUnix})
		}
	}
	return out, nil
}

func (d *fakeDownloads) Each(_ context.Context, _ string, files []importer.File, fn func(importer.File, io.Reader) error) error {
	for _, f := range files {
		if err := fn(f, strings.NewReader("x")); err != nil {
			return err
		}
	}
	return nil
}

type fakeLibrary struct{ files map[string]int64 }

func (l *fakeLibrary) Stat(_ context.Context, rel string) (int64, bool, error) {
	if s, ok := l.files[rel]; ok {
		return s, true, nil
	}
	for p := range l.files {
		if strings.HasPrefix(p, rel+"/") {
			return 0, true, nil // a folder
		}
	}
	return 0, false, nil
}

func (l *fakeLibrary) Write(_ context.Context, rel string, r io.Reader, _ int64) error {
	if _, ok := l.files[rel]; ok {
		return errors.New("exists")
	}
	b, _ := io.ReadAll(r)
	l.files[rel] = int64(len(b))
	return nil
}

func (l *fakeLibrary) Creators(context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for p := range l.files {
		c := strings.SplitN(p, "/", 2)[0]
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

type memLog struct {
	recs      map[string]ImportRecord
	baselined bool
}

func (m *memLog) ImportRecords(context.Context) ([]ImportRecord, error) {
	var out []ImportRecord
	for _, r := range m.recs {
		out = append(out, r)
	}
	return out, nil
}
func (m *memLog) SaveImport(_ context.Context, r ImportRecord) error {
	m.recs[r.Source] = r
	return nil
}
func (m *memLog) ImportBaselined(context.Context) (bool, error) { return m.baselined, nil }
func (m *memLog) SetImportBaselined(context.Context) error      { m.baselined = true; return nil }

func (l *fakeLibrary) list() string {
	var out []string
	for p := range l.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestImporterBaselinesThenImportsNewCompleteFolders(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	old := now.Add(-3 * time.Hour).Unix()
	dl := &fakeDownloads{
		files: []importer.File{
			{Rel: "nomnom/Old Model/STL/75mm/o.stl", Size: 1, ModUnix: old},
		},
		archives: map[string][]string{},
		broken:   map[string]bool{},
	}
	lib := &fakeLibrary{files: map[string]int64{"nomnom/Kida/75mm/k.stl": 1}}
	log := &memLog{recs: map[string]ImportRecord{}}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, Now: func() time.Time { return now }}
	ctx := context.Background()

	// First run: only records what's there.
	sum, err := im.Run(ctx)
	if err != nil || sum.Baselined != 1 || log.recs["nomnom/Old Model"].State != ImportExisting {
		t.Fatalf("baseline: %+v %v %+v", sum, err, log.recs)
	}

	// A new folder: one finished zip, one still being written; then quiet.
	dl.files = append(dl.files,
		importer.File{Rel: "wicked/ Panther Bust/Wicked - Panther Bust (Non Supported).zip", Size: 9, ModUnix: now.Add(-5 * time.Minute).Unix()},
		importer.File{Rel: "wicked/ Panther Bust/Wicked - Panther Bust (One Piece).zip", Size: 9, ModUnix: old},
	)
	dl.archives["wicked/ Panther Bust/Wicked - Panther Bust (Non Supported).zip"] = []string{"head.stl", "body.stl"}
	dl.archives["wicked/ Panther Bust/Wicked - Panther Bust (One Piece).zip"] = []string{"panther.stl"}
	if sum, _ := im.Run(ctx); sum.Waiting != 1 || sum.Imported != 0 {
		t.Fatalf("recently changed folder: %+v", sum)
	}
	now = now.Add(2 * time.Hour)
	dl.broken = map[string]bool{"wicked/ Panther Bust/Wicked - Panther Bust (One Piece).zip": true}
	if sum, _ := im.Run(ctx); sum.Waiting != 1 || log.recs["wicked/ Panther Bust"].State != ImportWaiting {
		t.Fatalf("broken archive: %+v %+v", sum, log.recs["wicked/ Panther Bust"])
	}
	dl.broken = map[string]bool{}
	sum, err = im.Run(ctx)
	if err != nil || sum.Imported != 1 || sum.Files != 3 {
		t.Fatalf("import: %+v %v", sum, err)
	}
	want := "Wicked/Panther Bust/No Supports Combined/panther.stl\nWicked/Panther Bust/No Supports/body.stl\nWicked/Panther Bust/No Supports/head.stl\nnomnom/Kida/75mm/k.stl"
	if lib.list() != want {
		t.Errorf("library:\n%s\nwant:\n%s", lib.list(), want)
	}
	// Nothing changed: nothing happens.
	if sum, _ := im.Run(ctx); sum.Imported+sum.Files != 0 {
		t.Errorf("second run: %+v", sum)
	}
	// A new file arrives later: only it is copied, into the same folder.
	dl.files = append(dl.files, importer.File{Rel: "wicked/ Panther Bust/extra.stl", Size: 1, ModUnix: now.Add(-2 * time.Hour).Unix()})
	if sum, _ := im.Run(ctx); sum.Files != 1 || !strings.Contains(lib.list(), "Wicked/Panther Bust/No Supports/extra.stl") {
		t.Errorf("update: %+v\n%s", sum, lib.list())
	}

	// The existing folder, on request; its name is taken in the library.
	lib.files["nomnom/Old Model/readme.txt"] = 5
	if err := im.Request(ctx, "nomnom/Old Model"); err != nil {
		t.Fatal(err)
	}
	if sum, _ := im.Run(ctx); sum.Imported != 1 || !strings.Contains(lib.list(), "nomnom/Old Model (2)/75mm/o.stl") {
		t.Errorf("requested: %+v\n%s", sum, lib.list())
	}
	if err := im.Request(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown request: %v", err)
	}
}

func TestImporterKeepsBothWhenAFileOfTheSameNameDiffers(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{}}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, Now: func() time.Time { return now }}
	if _, err := im.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	lib.files["Nomnom/Kida/75mm/k.stl"] = 99 // changed by someone since
	dl.files[0].ModUnix = 2                  // and the download changed too
	if _, err := im.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lib.list(), "Nomnom/Kida/75mm/k (imported).stl") {
		t.Errorf("library:\n%s", lib.list())
	}
}
