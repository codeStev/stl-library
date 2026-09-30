package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Print states.
const (
	JobPlanned = "planned"
	JobPrinted = "printed"
)

// Job is a "print": a named set of parts (with counts) printed together,
// with the plates (sliced files) used for it. Its parts can come from any
// models; "printed X of Y parts" of a model is counted from the printed jobs.
type Job struct {
	ID          int64
	Name, Note  string
	State       string
	CreatedUnix int64
	PrintedUnix int64
	Items       []JobItem
	Plates      []JobPlate
}

// JobItem is a part of a job. Missing: the file is not in the library any
// more (the link stays, in case it comes back).
type JobItem struct {
	PartID    int64
	Path      string
	ModelID   int64
	ModelName string
	Count     int
	Missing   bool
}

// JobPlate is a sliced file of a job: a library file (PartID) or an upload.
type JobPlate struct {
	PartID   int64
	UploadID string
	Path     string
	Name     string
	Size     int64
	Missing  bool
}

// JobSummary is a job as listed.
type JobSummary struct {
	ID          int64
	Name        string
	State       string
	Items       int // part files
	Copies      int // parts counting their copies
	Plates      int
	CreatedUnix int64
	PrintedUnix int64
}

// PlateRef names a plate: a library file or an upload.
type PlateRef struct {
	PartID   int64
	UploadID string
}

// Upload is a sliced file someone uploaded; it lives in the app's data.
type Upload struct {
	ID          string // sha256 of the content
	Name        string
	Size        int64
	CreatedUnix int64
}

// PlateFiles stores uploaded plates (outside the library).
type PlateFiles interface {
	// Save stores what r delivers (at most max bytes) and returns its id (sha256) and size.
	Save(ctx context.Context, r io.Reader, max int64) (id string, size int64, err error)
	// Open returns an uploaded file.
	Open(ctx context.Context, id string) (io.ReadSeekCloser, int64, error)
}

// ErrTooLarge is returned when an upload exceeds the limit.
var ErrTooLarge = errors.New("the upload is too large")

const (
	maxJobParts   = 1000
	maxJobPlates  = 200
	maxUploadSize = 4 << 30
)

// Jobs manages prints.
type Jobs struct {
	Store Store
	Files PlateFiles
	Now   func() time.Time
}

func (j Jobs) now() int64 {
	if j.Now != nil {
		return j.Now().Unix()
	}
	return time.Now().Unix()
}

func cleanJob(name, note string) (string, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	note = strings.TrimSpace(note)
	if name == "" || utf8.RuneCountInString(name) > 100 || utf8.RuneCountInString(note) > maxText {
		return "", "", ErrInvalid
	}
	return name, note, nil
}

func (j Jobs) List(ctx context.Context) ([]JobSummary, error) { return j.Store.Jobs(ctx) }

func (j Jobs) Get(ctx context.Context, id int64) (*Job, error) { return j.Store.Job(ctx, id) }

// Create makes a planned job.
func (j Jobs) Create(ctx context.Context, name, note string) (int64, error) {
	name, note, err := cleanJob(name, note)
	if err != nil {
		return 0, err
	}
	return j.Store.CreateJob(ctx, name, note, j.now())
}

// Update renames a job, changes its note and its state. A job turned
// "printed" gets the time; turned back to "planned" it loses it.
func (j Jobs) Update(ctx context.Context, id int64, name, note, state string) error {
	name, note, err := cleanJob(name, note)
	if err != nil || (state != JobPlanned && state != JobPrinted) {
		return ErrInvalid
	}
	var at int64
	if state == JobPrinted {
		at = j.now()
		if cur, err := j.Store.Job(ctx, id); err == nil && cur.State == JobPrinted {
			at = cur.PrintedUnix
		}
	}
	return j.Store.UpdateJob(ctx, id, name, note, state, at)
}

func (j Jobs) Delete(ctx context.Context, id int64) error { return j.Store.DeleteJob(ctx, id) }

// SetItems replaces the parts of a job; a part is listed once (counts add up).
func (j Jobs) SetItems(ctx context.Context, id int64, items []SliceItem) error {
	if len(items) > maxJobParts {
		return ErrInvalid
	}
	merged := map[int64]int{}
	var order []int64
	for _, it := range items {
		if it.Count < 1 || it.Count > maxSliceCount {
			return ErrInvalid
		}
		if _, ok := merged[it.PartID]; !ok {
			order = append(order, it.PartID)
		}
		merged[it.PartID] += it.Count
	}
	out := make([]SliceItem, 0, len(order))
	for _, p := range order {
		if merged[p] > maxSliceCount {
			return ErrInvalid
		}
		out = append(out, SliceItem{PartID: p, Count: merged[p]})
	}
	return j.Store.SetJobItems(ctx, id, out)
}

// SetPlates replaces the plates of a job.
func (j Jobs) SetPlates(ctx context.Context, id int64, plates []PlateRef) error {
	if len(plates) > maxJobPlates {
		return ErrInvalid
	}
	seen := map[PlateRef]bool{}
	var out []PlateRef
	for _, p := range plates {
		if (p.PartID == 0) == (p.UploadID == "") {
			return ErrInvalid // exactly one of them
		}
		if p.PartID != 0 {
			ref, err := j.Store.Part(ctx, p.PartID)
			if err != nil {
				return err
			}
			if !IsSliced(ref.Path) {
				return ErrInvalid
			}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return j.Store.SetJobPlates(ctx, id, out)
}

// UploadPlate stores a sliced file someone uploaded and records it.
func (j Jobs) UploadPlate(ctx context.Context, name string, r io.Reader) (Upload, error) {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || utf8.RuneCountInString(name) > 200 || !IsSliced(name) || j.Files == nil {
		return Upload{}, ErrInvalid
	}
	id, size, err := j.Files.Save(ctx, r, maxUploadSize)
	if err != nil {
		return Upload{}, err
	}
	u := Upload{ID: id, Name: name, Size: size, CreatedUnix: j.now()}
	return u, j.Store.AddUpload(ctx, u)
}
