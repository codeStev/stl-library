package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
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
	content  string              // what every file contains ("x" when empty)
	removed  []string            // unit/rel of removed files
}

func (d *fakeDownloads) Remove(_ context.Context, unit string, rels []string) error {
	gone := map[string]bool{}
	for _, r := range rels {
		d.removed = append(d.removed, unit+"/"+r)
		gone[unit+"/"+r] = true
	}
	var keep []importer.File
	for _, f := range d.files {
		if !gone[f.Rel] {
			keep = append(keep, f)
		}
	}
	d.files = keep
	return nil
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
	c := d.content
	if c == "" {
		c = "x"
	}
	for _, f := range files {
		if err := fn(f, strings.NewReader(c)); err != nil {
			return err
		}
	}
	return nil
}

type fakeLibrary struct {
	files   map[string]int64
	content map[string]string // what was written (other files: unknown content)
	corrupt bool              // Hash reports something else than was written
}

func (l *fakeLibrary) FindSame(_ context.Context, dir, name string, size int64) ([]string, error) {
	var out []string
	for p, sz := range l.files {
		if strings.HasPrefix(p, dir+"/") && path.Base(p) == name && sz == size {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *fakeLibrary) Hash(_ context.Context, rel string) (string, error) {
	c, ok := l.content[rel]
	if !ok {
		c = strings.Repeat("?", int(l.files[rel]))
	}
	if l.corrupt {
		c += "!"
	}
	h := sha256.Sum256([]byte(c))
	return hex.EncodeToString(h[:]), nil
}

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
	if l.content == nil {
		l.content = map[string]string{}
	}
	l.content[rel] = string(b)
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

func TestImporterDeletesVerifiedImportsFromTheDownloads(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{
		{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1},
		{Rel: "nomnom/Kida/STL/75mm/base.stl", Size: 1, ModUnix: 1},
	}}
	lib := &fakeLibrary{files: map[string]int64{}}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, DeleteImported: true, Now: func() time.Time { return now }}
	sum, err := im.Run(context.Background())
	if err != nil || sum.Imported != 1 || sum.Removed != 1 || len(dl.removed) != 2 || len(dl.files) != 0 {
		t.Fatalf("%+v %v removed %v", sum, err, dl.removed)
	}
	if r := log.recs["nomnom/Kida"]; !strings.Contains(r.Message, "removed from the downloads") {
		t.Errorf("record: %+v", r)
	}
}

func TestImporterKeepsTheDownloadWhenTheCopyDoesntVerify(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{}, corrupt: true}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, DeleteImported: true, Now: func() time.Time { return now }}
	sum, _ := im.Run(context.Background())
	if sum.Failed != 1 || len(dl.removed) != 0 || !strings.Contains(log.recs["nomnom/Kida"].Message, "differs") {
		t.Errorf("%+v removed %v record %+v", sum, dl.removed, log.recs["nomnom/Kida"])
	}
}

func TestImporterComparesContentNotJustSize(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	ctx := context.Background()
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{}}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, Now: func() time.Time { return now }}
	if _, err := im.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// The download changes (same size, other content): the library copy
	// counts as a different file, not as "already there".
	dl.content, dl.files[0].ModUnix = "y", 2
	im.DeleteImported = true
	sum, err := im.Run(ctx)
	if err != nil || sum.Files != 1 || lib.content["Nomnom/Kida/75mm/k (imported).stl"] != "y" || len(dl.removed) != 1 {
		t.Errorf("%+v %v\n%s removed %v", sum, err, lib.list(), dl.removed)
	}
	// Again, identical content: nothing written, and it's removed.
	dl.files = []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 3}}
	dl.removed = nil
	if sum, err := im.Run(ctx); err != nil || sum.Files != 0 || sum.Removed != 1 || len(dl.removed) != 1 {
		t.Errorf("identical: %+v %v %s", sum, err, lib.list())
	}
}

func TestImporterPutsABustIntoItsExistingModel(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Busts/Kida - Atlantis/Supported STL/b.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{"Nomnom/Kida - Atlantis/75mm/Supported/k.stl": 1}}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, Now: func() time.Time { return now }}
	if sum, err := im.Run(context.Background()); err != nil || sum.Imported != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if !strings.Contains(lib.list(), "Nomnom/Kida - Atlantis/Bust/Supported/b.stl") || strings.Contains(lib.list(), "(2)") {
		t.Errorf("library:\n%s", lib.list())
	}
}

func TestImporterAdoptsExistingFoldersWhenAsked(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{}}
	log := &memLog{recs: map[string]ImportRecord{}}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, AdoptExisting: true, Now: func() time.Time { return now }}
	sum, err := im.Run(context.Background())
	if err != nil || sum.Imported != 1 || sum.Baselined != 0 || !strings.Contains(lib.list(), "Nomnom/Kida/75mm/k.stl") {
		t.Errorf("%+v %v\n%s", sum, err, lib.list())
	}
}

func TestImporterMergesIntoAnExistingModelWhenAsked(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	mk := func(merge bool) (*Importer, *fakeLibrary) {
		dl := &fakeDownloads{files: []importer.File{{Rel: "Bulkamancer/Lyn/lyn_no_supports.zip/lyn.stl", Size: 1, ModUnix: 1},
			{Rel: "Bulkamancer/Lyn/lyn_no_supports.zip/base.stl", Size: 1, ModUnix: 1}}}
		lib := &fakeLibrary{files: map[string]int64{"Bulkamancer/Lyn/Supported STL/lyn.stl": 1}}
		return &Importer{Downloads: dl, Library: lib, Log: &memLog{recs: map[string]ImportRecord{}, baselined: true},
			Settle: time.Hour, MergeExisting: merge, Now: func() time.Time { return now }}, lib
	}
	im, lib := mk(true)
	if _, err := im.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lib.list(), "(2)") || !strings.Contains(lib.list(), "Bulkamancer/Lyn/No Supports/base.stl") {
		t.Errorf("merge:\n%s", lib.list())
	}
	im, lib = mk(false)
	im.Run(context.Background())
	if !strings.Contains(lib.list(), "Bulkamancer/Lyn (2)/") {
		t.Errorf("without merge:\n%s", lib.list())
	}
}

func TestMergeSkipsAFileThatIsInTheModelUnderAnotherVariantFolder(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	dl := &fakeDownloads{files: []importer.File{{Rel: "Bulkamancer/Lyn/lyn_pre_supported_stl.zip/lyn.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{"Bulkamancer/Lyn/Supported STL/lyn.stl": 1}, content: map[string]string{"Bulkamancer/Lyn/Supported STL/lyn.stl": "x"}}
	im := &Importer{Downloads: dl, Library: lib, Log: &memLog{recs: map[string]ImportRecord{}, baselined: true},
		Settle: time.Hour, MergeExisting: true, DeleteImported: true, Now: func() time.Time { return now }}
	sum, err := im.Run(context.Background())
	if err != nil || sum.Files != 0 || sum.Removed != 1 || strings.Contains(lib.list(), "Bulkamancer/Lyn/Supported/") {
		t.Fatalf("same file under another name: %+v %v\n%s", sum, err, lib.list())
	}
	// Same name and size, other content: it is a different file and gets written.
	dl.files = []importer.File{{Rel: "Bulkamancer/Lyn/lyn_pre_supported_lys.zip/lyn.stl", Size: 1, ModUnix: 2}}
	dl.content = "y"
	sum, err = im.Run(context.Background())
	if err != nil || sum.Files != 1 || lib.content["Bulkamancer/Lyn/Supported/lyn.stl"] != "y" {
		t.Errorf("different content: %+v %v\n%s", sum, err, lib.list())
	}
}

func TestManyDifferentFilesWithOneNameAreAllKept(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	ctx := context.Background()
	dl := &fakeDownloads{files: []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 1}}}
	lib := &fakeLibrary{files: map[string]int64{}}
	log := &memLog{recs: map[string]ImportRecord{}, baselined: true}
	im := &Importer{Downloads: dl, Library: lib, Log: log, Settle: time.Hour, DeleteImported: true, Now: func() time.Time { return now }}
	for i, content := range []string{"x", "y", "z", "w"} {
		dl.content = content
		dl.files = []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: int64(i + 1)}}
		if sum, err := im.Run(ctx); err != nil || sum.Files != 1 {
			t.Fatalf("content %q: %+v %v\n%s", content, sum, err, lib.list())
		}
	}
	want := map[string]string{
		"Nomnom/Kida/75mm/k.stl":              "x",
		"Nomnom/Kida/75mm/k (imported).stl":   "y",
		"Nomnom/Kida/75mm/k (imported 2).stl": "z",
		"Nomnom/Kida/75mm/k (imported 3).stl": "w",
	}
	for p, c := range want {
		if lib.content[p] != c {
			t.Errorf("%s = %q, want %q\n%s", p, lib.content[p], c, lib.list())
		}
	}
	// Coming again with content that is in the library already (under an
	// alternative name) writes nothing.
	dl.content = "z"
	dl.files = []importer.File{{Rel: "nomnom/Kida/STL/75mm/k.stl", Size: 1, ModUnix: 9}}
	if sum, err := im.Run(ctx); err != nil || sum.Files != 0 || sum.Removed != 1 {
		t.Errorf("known content: %+v %v\n%s", sum, err, lib.list())
	}
}
