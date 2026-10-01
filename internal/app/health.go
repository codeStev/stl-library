package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
	"time"
)

// HashJob is a library file to hash (or to re-check).
type HashJob struct {
	Path    string
	Size    int64
	ModUnix int64
}

// DupFile is a file of a duplicate group, with the model it belongs to.
type DupFile struct {
	PartID    int64
	Path      string
	ModelID   int64
	ModelName string
	Linked    bool // already a hard link to another copy: takes no space of its own
}

// DupGroup is a set of files with exactly the same content.
type DupGroup struct {
	SHA256 string
	Size   int64
	Files  []DupFile
}

// Wasted is the space the extra copies take.
func (g DupGroup) Wasted() int64 {
	copies := 0
	for _, f := range g.Files {
		if !f.Linked {
			copies++
		}
	}
	if copies < 2 {
		return 0
	}
	return int64(copies-1) * g.Size
}

// HealthEvent is something the integrity check found.
type HealthEvent struct {
	ID     int64
	Kind   string // "corrupt": the content changed though size and date did not; "missing": known content is gone
	Path   string
	Detail string
	AtUnix int64
	// OtherCopies (for "corrupt") are other library files with the content the file had.
	OtherCopies []string
}

// HealthCounts says how far the hashing is.
type HealthCounts struct {
	Files  int // files in the library
	Hashed int // of them with a known hash
}

// HealthState is what the background hasher is doing.
type HealthState struct {
	Running bool
	Paused  bool
	Current string
	Done    int // hashed in this run
	Errors  int // files that could not be read
}

// verifyAfter is how long a file's hash is trusted before it is read again to check the file.
const verifyAfter = 90 * 24 * time.Hour

// Health hashes the library's files in the background (low priority: the process runs niced),
// for the duplicate finder and the integrity check.
type Health struct {
	Store Store
	Files Files
	Now   func() time.Time
	// Pause is how long the hasher rests between files, to go easy on the disks.
	Pause time.Duration

	mu     sync.Mutex
	state  HealthState
	wakeup chan struct{}
}

func (h *Health) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Health) init() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wakeup == nil {
		h.wakeup = make(chan struct{}, 1)
	}
}

// State returns what the hasher is doing.
func (h *Health) State() HealthState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Resume lets the hasher work (again) and wakes it.
func (h *Health) Resume() {
	h.init()
	h.mu.Lock()
	h.state.Paused = false
	h.mu.Unlock()
	select {
	case h.wakeup <- struct{}{}:
	default:
	}
}

// Stop makes the hasher rest after the file it is on.
func (h *Health) Stop() {
	h.mu.Lock()
	h.state.Paused = true
	h.mu.Unlock()
}

func (h *Health) paused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state.Paused
}

func (h *Health) set(f func(*HealthState)) {
	h.mu.Lock()
	f(&h.state)
	h.mu.Unlock()
}

// ctxReader stops a long read when the context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (h *Health) hash(ctx context.Context, path string) (string, error) {
	rc, err := h.Files.Open(ctx, path)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	s := sha256.New()
	if _, err := io.Copy(s, ctxReader{ctx, rc}); err != nil {
		return "", err
	}
	return hex.EncodeToString(s.Sum(nil)), nil
}

// Step hashes one batch: new and changed files first, then files whose hash is old enough to check
// again. It returns how many files it handled (0: nothing to do).
func (h *Health) Step(ctx context.Context, batch int) (int, error) {
	n := 0
	jobs, err := h.Store.FilesToHash(ctx, batch)
	if err != nil {
		return 0, err
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil || h.paused() {
			return n, ctx.Err()
		}
		h.set(func(s *HealthState) { s.Current = j.Path })
		sum, err := h.hash(ctx, j.Path)
		if err != nil {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			h.set(func(s *HealthState) { s.Errors++ })
			continue // unreadable now: tried again in a later run
		}
		if err := h.Store.SaveHash(ctx, j, sum, h.now().Unix()); err != nil {
			return n, err
		}
		n++
		h.set(func(s *HealthState) { s.Done++ })
		if h.Pause > 0 {
			time.Sleep(h.Pause)
		}
	}
	if len(jobs) > 0 {
		return n, nil
	}
	// Everything is hashed: check the oldest hashes against the files.
	jobs, err = h.Store.FilesToVerify(ctx, h.now().Add(-verifyAfter).Unix(), batch)
	if err != nil {
		return 0, err
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil || h.paused() {
			return n, ctx.Err()
		}
		h.set(func(s *HealthState) { s.Current = j.Path })
		sum, err := h.hash(ctx, j.Path)
		if err != nil {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			h.set(func(s *HealthState) { s.Errors++ })
			continue
		}
		if _, err := h.Store.Verified(ctx, j, sum, h.now().Unix()); err != nil {
			return n, err
		}
		n++
		if h.Pause > 0 {
			time.Sleep(h.Pause)
		}
	}
	return n, nil
}

// Run hashes until ctx ends: it works while there is something to do, then waits for a wake-up
// (a finished scan, or Resume) or an hour.
func (h *Health) Run(ctx context.Context) {
	h.init()
	for {
		if !h.paused() {
			h.set(func(s *HealthState) { s.Running = true })
			n, err := h.Step(ctx, 50)
			if ctx.Err() != nil {
				return
			}
			if err == nil && n > 0 {
				continue
			}
			h.Store.PruneHashes(ctx)
			h.set(func(s *HealthState) { s.Running, s.Current, s.Done = false, "", 0 })
		} else {
			h.set(func(s *HealthState) { s.Running, s.Current = false, "" })
		}
		select {
		case <-ctx.Done():
			return
		case <-h.wakeup:
		case <-time.After(time.Hour):
		}
	}
}

// Wake tells the hasher that the library changed (a scan finished).
func (h *Health) Wake() {
	h.init()
	select {
	case h.wakeup <- struct{}{}:
	default:
	}
}

// Duplicates lists groups of identical files (only files of at least minSize bytes), the biggest waste first.
func (h *Health) Duplicates(ctx context.Context, minSize int64, limit, offset int) ([]DupGroup, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return h.Store.DuplicateGroups(ctx, minSize, limit, offset)
}
