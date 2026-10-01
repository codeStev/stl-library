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
	"sync"
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
	// FindSame lists the files below dir with that name and size (any
	// depth): candidates for "this file is there already".
	FindSame(ctx context.Context, dir, name string, size int64) ([]string, error)
}

// Import states of a download folder.
const (
	ImportExisting  = "existing" // was there before importing started; imported only on request
	ImportWaiting   = "waiting"  // not complete yet
	ImportQueued    = "queued"   // requested by the user, imported once complete
	ImportDone      = "imported"
	ImportDuplicate = "duplicate" // every model file is in the library already (by content): not imported
	ImportFailed    = "failed"
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

// HashIndex knows the content hashes of the library's files (found by the
// background hasher).
type HashIndex interface {
	// KnownHashes returns which of the given SHA-256 sums (hex) belong to a
	// library file.
	KnownHashes(ctx context.Context, sums []string) (map[string]bool, error)
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
	// AdoptExisting imports what is in the downloads on the very first run
	// too, instead of only recording it as already there (for a downloads
	// folder that was staged on purpose, e.g. to unpack archives).
	AdoptExisting bool
	// MergeExisting imports into a model folder that already exists in the
	// library (files that are already there are skipped, others added)
	// instead of making a fresh "<Model> (2)" - for archives that belong
	// to a model whose other files are unpacked already. A file that is
	// already in the model under another variant folder (e.g. "Supported
	// STL" where the importer would write "Supported") counts as there.
	MergeExisting bool
	// Known, when set, lets the importer skip a download folder whose model
	// files (STL, OBJ, 3MF) are all in the library already, whatever their
	// names - e.g. a "last months models" drive that repeats earlier models.
	Known HashIndex
	Now   func() time.Time

	mu      sync.Mutex
	prog    ImportProgress
	trigger chan struct{}
	once    sync.Once
}

// ImportProgress is what a running import is doing right now.
type ImportProgress struct {
	Running   bool
	Current   string // the download folder being imported
	SinceUnix int64
	DoneUnits int // folders finished in this run
}

// Progress reports the current run, if any.
func (im *Importer) Progress() ImportProgress {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.prog
}

func (im *Importer) setProgress(f func(*ImportProgress)) {
	im.mu.Lock()
	defer im.mu.Unlock()
	f(&im.prog)
}

// Triggered fires when someone asks for a run right now (instead of
// waiting for the next interval).
func (im *Importer) Triggered() <-chan struct{} {
	im.once.Do(func() { im.trigger = make(chan struct{}, 1) })
	return im.trigger
}

// Trigger asks for a run right now; it never blocks.
func (im *Importer) Trigger() {
	im.Triggered()
	select {
	case im.trigger <- struct{}{}:
	default:
	}
}

// RetryFailed requests every failed folder again and returns how many.
func (im *Importer) RetryFailed(ctx context.Context) (int, error) {
	records, err := im.Log.ImportRecords(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range records {
		if r.State != ImportFailed {
			continue
		}
		r.State, r.Message, r.Signature = ImportQueued, "import requested", ""
		if err := im.save(ctx, r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ImportSummary counts the outcome of a run.
type ImportSummary struct {
	Imported, Waiting, Failed, Files int
	Duplicates                       int // folders skipped because the library has all their model files
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
	im.setProgress(func(p *ImportProgress) { *p = ImportProgress{Running: true, SinceUnix: im.now().Unix()} })
	defer im.setProgress(func(p *ImportProgress) { *p = ImportProgress{} })
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
	if !baselined && im.AdoptExisting {
		if err := im.Log.SetImportBaselined(ctx); err != nil {
			return sum, err
		}
		baselined = true
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
		case known && rec.State == ImportDuplicate && rec.Signature == sig:
			continue // imported anyway when requested
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
		im.setProgress(func(p *ImportProgress) { p.Current = u.Path })
		if rec.Target == "" && rec.State != ImportQueued && im.Known != nil { // a requested import is done regardless
			dup, err := im.isDuplicate(ctx, u)
			switch {
			case errors.Is(err, errIncomplete):
				rec.State, rec.Message = ImportWaiting, err.Error()
				sum.Waiting++
				if err := im.save(ctx, rec); err != nil {
					return sum, err
				}
				continue
			case err != nil:
				rec.State, rec.Message, rec.Signature = ImportFailed, err.Error(), sig
				sum.Failed++
				if err := im.save(ctx, rec); err != nil {
					return sum, err
				}
				continue
			case dup:
				rec.State, rec.Signature = ImportDuplicate, sig
				rec.Message = "all model files are in the library already - not imported"
				sum.Duplicates++
				if err := im.save(ctx, rec); err != nil {
					return sum, err
				}
				continue
			}
		}
		n, err := im.importUnit(ctx, u, &rec)
		im.setProgress(func(p *ImportProgress) { p.Current, p.DoneUnits = "", p.DoneUnits+1 })
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
	target, err := im.targetDir(ctx, modelDir, rec.Target, u.Bust)
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
	var differ, later []importer.File
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
			if im.MergeExisting {
				cands, err := im.Library.FindSame(ctx, target, path.Base(to), f.Size)
				if err != nil {
					return err
				}
				if len(cands) > 0 {
					same, err := im.sameAsAny(ctx, cands, r)
					if err != nil || same {
						return err // the same file is in this model already
					}
					later = append(later, f) // read to the end for the comparison: written in a second pass
					return nil
				}
			}
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
	if err == nil && len(later) > 0 {
		err = im.Downloads.Each(ctx, u.Path, later, func(f importer.File, r io.Reader) error {
			return write(f, dest[f.Rel], r)
		})
	}
	if err == nil && len(differ) > 0 {
		// Files whose name is taken by other content get "(imported)", "(imported 2)", ...
		// Which slot is free (or already holds this very content) is decided by the
		// file's hash, so the files are read once to hash them, then again to write.
		sums := map[string]string{}
		err = im.Downloads.Each(ctx, u.Path, differ, func(f importer.File, r io.Reader) error {
			h := sha256.New()
			if _, err := io.Copy(h, r); err != nil {
				return err
			}
			sums[f.Rel] = hex.EncodeToString(h.Sum(nil))
			return nil
		})
		if err == nil {
			err = im.Downloads.Each(ctx, u.Path, differ, func(f importer.File, r io.Reader) error {
				for n := 1; n <= maxAlternatives; n++ {
					to := alternative(dest[f.Rel], n)
					size, exists, err := im.Library.Stat(ctx, to)
					if err != nil {
						return err
					}
					if !exists {
						return write(f, to, r)
					}
					if size == f.Size {
						lib, err := im.Library.Hash(ctx, to)
						if err != nil {
							return err
						}
						if lib == sums[f.Rel] {
							return nil // imported before under this name
						}
					}
				}
				return fmt.Errorf("%s: more than %d different files with that name", dest[f.Rel], maxAlternatives)
			})
		}
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

// modelFile reports files that make up a model's content.
func modelFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".stl", ".obj", ".3mf":
		return true
	}
	return false
}

// isDuplicate reports whether the unit has model files and every one of
// them (compared by SHA-256) is in the library already.
func (im *Importer) isDuplicate(ctx context.Context, u importer.Unit) (bool, error) {
	files, err := im.Downloads.Expand(ctx, u.Path, u.Files)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errIncomplete, err)
	}
	var models []importer.File
	for _, f := range files {
		if !f.Hidden && modelFile(f.Rel) {
			models = append(models, f)
		}
	}
	if len(models) == 0 {
		return false, nil
	}
	var sums []string
	err = im.Downloads.Each(ctx, u.Path, models, func(f importer.File, r io.Reader) error {
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return err
		}
		sums = append(sums, hex.EncodeToString(h.Sum(nil)))
		return nil
	})
	if err != nil {
		return false, err
	}
	known, err := im.Known.KnownHashes(ctx, sums)
	if err != nil {
		return false, err
	}
	for _, s := range sums {
		if !known[s] {
			return false, nil
		}
	}
	return true, nil
}

type writtenFile struct{ to, sum string }

// sameAsAny reports whether r (read to the end) has the content of one of
// the library files.
func (im *Importer) sameAsAny(ctx context.Context, rels []string, r io.Reader) (bool, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	for _, rel := range rels {
		lib, err := im.Library.Hash(ctx, rel)
		if err != nil {
			return false, err
		}
		if lib == sum {
			return true, nil
		}
	}
	return false, nil
}

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
// this download, or a fresh one - "<Model> (2)" if the name is taken. A
// bust goes into the model of its name (it is that model's Bust scale).
func (im *Importer) targetDir(ctx context.Context, modelDir, previous string, bust bool) (string, error) {
	if previous != "" {
		return previous, nil
	}
	if bust || im.MergeExisting {
		return modelDir, nil
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

// maxAlternatives bounds how many different files may share one name.
const maxAlternatives = 50

// alternative is the n-th other name for a taken file name: "x (imported).stl",
// "x (imported 2).stl", ...
func alternative(p string, n int) string {
	ext := path.Ext(p)
	tag := " (imported)"
	if n > 1 {
		tag = fmt.Sprintf(" (imported %d)", n)
	}
	return strings.TrimSuffix(p, ext) + tag + ext
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
		p, err := im.previewUnit(ctx, u)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// PreviewUnit is Preview for one download folder.
func (im *Importer) PreviewUnit(ctx context.Context, source string) (UnitPreview, error) {
	files, err := im.Downloads.List(ctx)
	if err != nil {
		return UnitPreview{}, err
	}
	for _, u := range importer.Units(files) {
		if u.Path == source {
			return im.previewUnit(ctx, u)
		}
	}
	return UnitPreview{}, ErrNotFound
}

func (im *Importer) previewUnit(ctx context.Context, u importer.Unit) (UnitPreview, error) {
	p := UnitPreview{Source: u.Path}
	p.Settled, p.Why = importer.Settled(u, im.now(), im.Settle)
	expanded, err := im.Downloads.Expand(ctx, u.Path, u.Files)
	if err != nil {
		p.Err = err
		return p, nil
	}
	creatorDir, err := im.creatorDir(ctx, u.Creator)
	if err != nil {
		return p, err
	}
	p.Placements = importer.Placements(u, expanded, creatorDir)
	if len(p.Placements) > 0 {
		p.Target = modelDirOf(p.Placements[0].Target)
	}
	return p, nil
}
