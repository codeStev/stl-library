package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"github.com/codeStev/stl-library/internal/core/printer"
)

// Printer controls a network resin printer.
type Printer interface {
	Status(ctx context.Context) (printer.Status, error)
	// Upload sends a file to the printer's storage under name. open is
	// called for each pass over the file (the printer needs a checksum
	// before the data); progress reports bytes sent.
	Upload(ctx context.Context, name string, size int64, open func() (io.ReadCloser, error), progress func(sent int64)) error
	// Start prints a file on the printer's storage (a bare name is in the
	// internal storage, see printer.RemotePath).
	Start(ctx context.Context, name string) error
	// Files lists a folder of the printer's storage ("/" for the volumes).
	Files(ctx context.Context, dir string) ([]printer.File, error)
	// Delete removes files from the printer's storage.
	Delete(ctx context.Context, paths []string) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error
}

// ErrBusy is returned when the printer (or a transfer) is in the way.
var ErrBusy = errors.New("the printer is busy")

// Transfer is a file being (or last) sent to the printer.
type Transfer struct {
	PartID  int64
	File    string
	Size    int64
	Sent    int64
	Start   bool   // start printing when the upload is done
	State   string // "sending", "done", "printing" (started), "failed", "cancelled"
	Error   string
	Started time.Time
}

// Printing sends library files to the printer and relays its controls.
// One transfer at a time; it runs in the background, since uploads over
// WiFi are slow (a large file takes many minutes).
type Printing struct {
	Printer Printer
	Store   Store
	Files   Files

	mu       sync.Mutex
	transfer *Transfer
	cancel   context.CancelFunc
}

// Status is the printer's state and the current or last transfer.
func (p *Printing) Status(ctx context.Context) (printer.Status, *Transfer, error) {
	st, err := p.Printer.Status(ctx)
	p.mu.Lock()
	var t *Transfer
	if p.transfer != nil {
		c := *p.transfer
		t = &c
	}
	p.mu.Unlock()
	return st, t, err
}

// Send uploads a part file (.ctb/.goo) to the printer in the background,
// and starts printing it afterwards if start is set. It refuses while
// another transfer runs or the printer is printing.
func (p *Printing) Send(ctx context.Context, partID int64, start bool) (*Transfer, error) {
	part, err := p.Store.Part(ctx, partID)
	if err != nil {
		return nil, err
	}
	name := path.Base(part.Path)
	if !printer.Printable(name) {
		return nil, fmt.Errorf("%w: %s is not a sliced printer file (.ctb or .goo)", ErrInvalid, name)
	}
	st, err := p.Printer.Status(ctx)
	if err != nil {
		return nil, err
	}
	if st.Machine != printer.Idle {
		return nil, fmt.Errorf("%w: it is %s", ErrBusy, st.Machine)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.transfer != nil && p.transfer.State == "sending" {
		return nil, fmt.Errorf("%w: %s is still being sent", ErrBusy, p.transfer.File)
	}
	t := &Transfer{PartID: partID, File: name, Size: part.Size, Start: start, State: "sending", Started: time.Now()}
	p.transfer = t
	bg, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.run(bg, *part, t)
	c := *t
	return &c, nil
}

func (p *Printing) run(ctx context.Context, part FileRef, t *Transfer) {
	open := func() (io.ReadCloser, error) { return p.Files.Open(ctx, part.Path) }
	err := p.Printer.Upload(ctx, t.File, part.Size, open, func(sent int64) {
		p.mu.Lock()
		t.Sent = sent
		p.mu.Unlock()
	})
	if err == nil && t.Start {
		err = p.Printer.Start(ctx, t.File)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case errors.Is(err, context.Canceled):
		t.State = "cancelled"
	case err != nil:
		t.State, t.Error = "failed", err.Error()
	case t.Start:
		t.State = "printing"
	default:
		t.State = "done"
	}
}

// CancelTransfer stops a running upload.
func (p *Printing) CancelTransfer() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.transfer == nil || p.transfer.State != "sending" {
		return ErrNotFound
	}
	p.cancel()
	return nil
}

// Control pauses, resumes or stops the current print.
func (p *Printing) Control(ctx context.Context, action string) error {
	switch action {
	case "pause":
		return p.Printer.Pause(ctx)
	case "resume":
		return p.Printer.Resume(ctx)
	case "stop":
		return p.Printer.Stop(ctx)
	}
	return ErrInvalid
}
