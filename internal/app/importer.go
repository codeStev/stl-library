package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/core/importer"
)

// Downloads reads the downloads folder (e.g. where a downloader bot puts
// new models). It is only read, except that imported files are removed
// when the importer is asked to (Importer.DeleteImported).
type Downloads interface {
	// List returns every plain file below the downloads root (Rel relative
	// to the root).
	List(ctx context.Context) ([]importer.File, error)
	// Expand lists a unit's files with zip archives replaced by their
	// entries. An unreadable archive (still being written) is an error.
	Expand(ctx context.Context, unitPath string, files []importer.File) ([]importer.File, error)
	// Each streams the given files of a unit (plain files and archive
	// entries, as returned by Expand), opening each archive once.
	Each(ctx context.Context, unitPath string, files []importer.File, fn func(importer.File, io.Reader) error) error
	// Remove deletes the given files of a unit (Rel as listed, archives
	// themselves) and the folders that become empty, up to and including
	// the unit's folder - never above it.
	Remove(ctx context.Context, unitPath string, rels []string) error
}

// LibraryWriter adds files to the library. It never overwrites or deletes.
type LibraryWriter interface {
	// Stat reports whether a file or folder exists, and a file's size.
	Stat(ctx context.Context, rel string) (size int64, exists bool, err error)
	// Write creates a new file from r (written under a temporary name and
	// renamed when complete) with the given modification time; an
	// existing file is an error.
	Write(ctx context.Context, rel string, r io.Reader, modUnix int64) error
	// Creators lists the top-level folders of the library.
	Creators(ctx context.Context) ([]string, error)
	// Hash returns a file's SHA-256 (hex).
	Hash(ctx context.Context, rel string) (string, error)
}

// Import states of a download folder.
const (
	ImportExisting = "existing" // was there before importing started; imported only on request
	ImportWaiting  = "waiting"  // not complete yet
	ImportQueued   = "queued"   // requested by the user, imported once complete
	ImportDone     = "imported"
	ImportFailed   = "failed"
)

// ImportRecord is what happened to one download folder.
type ImportRecord struct {
	Source      string // unit path below the downloads root
	Signature   string
	State       string
	Target      string // model folder in the library
	Files       int    // files copied so far
	Message     string
	UpdatedUnix int64
}

// ImportLog keeps the import records.
type ImportLog interface {
	ImportRecords(ctx context.Context) ([]ImportRecord, error)
	SaveImport(ctx context.Context, r ImportRecord) error
	// ImportBaselined reports whether the first run (which only records
	// what is already there) has happened.
	ImportBaselined(ctx context.Context) (bool, error)
	SetImportBaselined(ctx context.Context) error
}

// Importer copies complete new download folders into the library,
// following the convention (see package importer). The library only ever
// gets new files. With DeleteImported, a download folder is removed from
// the downloads once every one of its files is verified (byte for byte)
// in the library; otherwise the downloads are left untouched.
type Importer struct {
	Downloads      Downloads
	Library        LibraryWriter
	Log            ImportLog
	Settle         time.Duration // how long a folder must be unchanged
	DeleteImported bool
	Now            func() time.Time
}

// ImportSummary counts the outcome of a run.
type ImportSummary struct {
	Imported, Waiting, Failed, Files int
	Removed                          int // imported folders removed from the downloads
	Baselined                        int // units recorded as existing on the first run
}

func (im *Importer) now() time.Time {
	if im.Now != nil {
		return im.Now()
	}
	return time.Now()
}

// Run looks at every download folder once. The very first run only
// records what is already there (state existing): those folders may well
// be in the library already. Later runs import new and changed folders
// once they are complete.
func (im *Importer) Run(ctx context.Context) (ImportSummary, error) {
	var sum ImportSummary
	files, err := im.Downloads.List(ctx)
	if err != nil {
		return sum, err
	}
	units := importer.Units(files)
	records, err := im.Log.ImportRecords(ctx)
	if err != nil {
		return sum, err
	}
	byPath := map[string]ImportRecord{}
	for _, r := range records {
		byPath[r.Source] = r
	}
	baselined, err := im.Log.ImportBaselined(ctx)
	if err != nil {
		return sum, err
	}
	if !baselined {
		for _, u := range units {
			if err := im.save(ctx, ImportRecord{Source: u.Path, Signature: importer.Signature(u.Files), State: ImportExisting,
				Message: "was in the downloads before importing started"}); err != nil {
				return sum, err
			}
			sum.Baselined++
		}
		return sum, im.Log.SetImportBaselined(ctx)
	}
	for _, u := range units {
		if ctx.Err() != nil {
			return sum, ctx.Err()
		}
		rec, known := byPath[u.Path]
		sig := importer.Signature(u.Files)
		switch {
		case known && rec.State == ImportExisting:
			continue
		case known && rec.State == ImportDone && rec.Signature == sig:
			continue
		case known && rec.State == ImportFailed && rec.Signature == sig:
			continue // retried when something changes, or on request
		}
		if !known {
			rec = ImportRecord{Source: u.Path}
		}
		if importer.NotAModel(u) {
			rec.State, rec.Signature = ImportFailed, sig
			rec.Message = "the folder name looks like a variant folder, not a model - put it into a model folder in the downloads"
			if err := im.save(ctx, rec); err != nil {
				return sum, err
			}
			sum.Failed++
			continue
		}
		if ok, why := importer.Settled(u, im.now(), im.Settle); !ok {
			if rec.State != ImportQueued {
				rec.State = ImportWaiting
			}
			rec.Message = why
			if err := im.save(ctx, rec); err != nil {
				return sum, err
			}
			sum.Waiting++
			continue
		}
		n, err := im.importUnit(ctx, u, &rec)
		sum.Files += n
		rec.Files += n
		switch {
		case errors.Is(err, errIncomplete):
			rec.State, rec.Message = ImportWaiting, err.Error()
			sum.Waiting++
		case err != nil:
			rec.State, rec.Message, rec.Signature = ImportFailed, err.Error(), sig
			sum.Failed++
		default:
			rec.State, rec.Signature = ImportDone, sig
			rec.Message = fmt.Sprintf("%d new files", n)
			sum.Imported++
			if im.DeleteImported {
				if err := im.Downloads.Remove(ctx, u.Path, sourceRels(u)); err != nil {
					rec.Message += "; removing it from the downloads failed: " + err.Error()
				} else {
					rec.Message += ", removed from the downloads"
					sum.Removed++
				}
			}
		}
		if err := im.save(ctx, rec); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// Request marks a download folder for import (e.g. one that existed
// before importing started, or one that failed); the next run imports it
// once it is complete.
func (im *Importer) Request(ctx context.Context, source string) error {
	records, err := im.Log.ImportRecords(ctx)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Source == source {
			r.State, r.Message, r.Signature = ImportQueued, "import requested", ""
			return im.save(ctx, r)
		}
	}
	return ErrNotFound
}

func (im *Importer) save(ctx context.Context, r ImportRecord) error {
	r.UpdatedUnix = im.now().Unix()
	return im.Log.SaveImport(ctx, r)
}

var errIncomplete = errors.New("an archive is not complete yet")

// importUnit copies a unit's files that aren't in the library yet.
func (im *Importer) importUnit(ctx context.Context, u importer.Unit, rec *ImportRecord) (int, error) {
	files, err := im.Downloads.Expand(ctx, u.Path, u.Files)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errIncomplete, err)
	}
	creatorDir, err := im.creatorDir(ctx, u.Creator)
	if err != nil {
		return 0, err
	}
	placements := importer.Placements(u, files, creatorDir)
	if len(placements) == 0 {
		return 0, nil
	}
	modelDir := modelDirOf(placements[0].Target)
	target, err := im.targetDir(ctx, modelDir, rec.Target)
	if err != nil {
		return 0, err
	}
	rec.Target = target
	dest := map[string]string{} // file Rel -> library path
	for _, p := range placements {
		dest[p.File.Rel] = target + strings.TrimPrefix(p.Target, modelDir)
	}
	// Every file ends up verified in the library: copied (and read back
	// when the downloads are to be deleted), or found there already with
	// the same content. A same-named file with other content is kept and
	// the download is written next to it (a second pass, as its stream
	// was used up comparing).
	copied := 0
	var differ []importer.File
	var written []writtenFile
	write := func(f importer.File, to string, r io.Reader) error {
		h := sha256.New()
		if err := im.Library.Write(ctx, to, io.TeeReader(r, h), f.ModUnix); err != nil {
			return fmt.Errorf("writing %s: %w", to, err)
		}
		written = append(written, writtenFile{to, hex.EncodeToString(h.Sum(nil))})
		copied++
		return nil
	}
	err = im.Downloads.Each(ctx, u.Path, files, func(f importer.File, r io.Reader) error {
		to := dest[f.Rel]
		size, exists, err := im.Library.Stat(ctx, to)
		if err != nil {
			return err
		}
		if !exists {
			return write(f, to, r)
		}
		if size == f.Size {
			same, err := im.sameContent(ctx, to, r)
			if err != nil || same {
				return err // already there
			}
		}
		differ = append(differ, f)
		return nil
	})
	if err == nil && len(differ) > 0 {
		err = im.Downloads.Each(ctx, u.Path, differ, func(f importer.File, r io.Reader) error {
			to := alternative(dest[f.Rel])
			size, exists, err := im.Library.Stat(ctx, to)
			if err != nil {
				return err
			}
			if !exists {
				return write(f, to, r)
			}
			if size == f.Size {
				if same, err := im.sameContent(ctx, to, r); err != nil || same {
					return err // imported before under the other name
				}
			}
			return fmt.Errorf("%s and %s both exist with other content", dest[f.Rel], to)
		})
	}
	if err == nil && im.DeleteImported {
		for _, w := range written {
			got, herr := im.Library.Hash(ctx, w.to)
			if herr != nil {
				return copied, herr
			}
			if got != w.sum {
				return copied, fmt.Errorf("%s differs from the download after copying", w.to)
			}
		}
	}
	return copied, err
}

type writtenFile struct{ to, sum string }

// sameContent reports whether r (read to the end) has the same content as
// the library file rel.
func (im *Importer) sameContent(ctx context.Context, rel string, r io.Reader) (bool, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return false, err
	}
	lib, err := im.Library.Hash(ctx, rel)
	return lib == hex.EncodeToString(h.Sum(nil)), err
}

// sourceRels are a unit's files as they sit in the downloads (archives,
// not their entries).
func sourceRels(u importer.Unit) []string {
	var out []string
	for _, f := range u.Files {
		if !f.Hidden {
			out = append(out, f.Rel)
		}
	}
	return out
}

// creatorDir maps a downloads creator folder ("nomnom", "wicked") to the
// library's creator folder: an existing one when the names match ignoring
// case and punctuation, else the name with a capital first letter.
func (im *Importer) creatorDir(ctx context.Context, creator string) (string, error) {
	existing, err := im.Library.Creators(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range existing {
		if fold(c) == fold(creator) {
			return c, nil
		}
	}
	c := strings.TrimSpace(creator)
	if c != "" && c == strings.ToLower(c) {
		c = strings.ToUpper(c[:1]) + c[1:]
	}
	return c, nil
}

// targetDir is the model folder to import into: the one used before for
// this download, or a fresh one - "<Model> (2)" if the name is taken.
func (im *Importer) targetDir(ctx context.Context, modelDir, previous string) (string, error) {
	if previous != "" {
		return previous, nil
	}
	for i := 1; ; i++ {
		dir := modelDir
		if i > 1 {
			dir = fmt.Sprintf("%s (%d)", modelDir, i)
		}
		_, exists, err := im.Library.Stat(ctx, dir)
		if err != nil || !exists {
			return dir, err
		}
	}
}

// modelDirOf: "<Creator>/<Model>/…" -> "<Creator>/<Model>".
func modelDirOf(target string) string {
	parts := strings.SplitN(target, "/", 3)
	return parts[0] + "/" + parts[1]
}

func alternative(p string) string {
	ext := path.Ext(p)
	return strings.TrimSuffix(p, ext) + " (imported)" + ext
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// UnitPreview is what a run would do with one download folder.
type UnitPreview struct {
	Source     string
	Settled    bool
	Why        string // why not settled
	Target     string
	Placements []importer.Placement
	Err        error
}

// Preview works out, without writing anything, where the files of every
// download folder would go.
func (im *Importer) Preview(ctx context.Context) ([]UnitPreview, error) {
	files, err := im.Downloads.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []UnitPreview
	for _, u := range importer.Units(files) {
		p := UnitPreview{Source: u.Path}
		p.Settled, p.Why = importer.Settled(u, im.now(), im.Settle)
		expanded, err := im.Downloads.Expand(ctx, u.Path, u.Files)
		if err != nil {
			p.Err = err
			out = append(out, p)
			continue
		}
		creatorDir, err := im.creatorDir(ctx, u.Creator)
		if err != nil {
			return nil, err
		}
		p.Placements = importer.Placements(u, expanded, creatorDir)
		if len(p.Placements) > 0 {
			p.Target = modelDirOf(p.Placements[0].Target)
		}
		out = append(out, p)
	}
	return out, nil
}
