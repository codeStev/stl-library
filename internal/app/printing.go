package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
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

// PrinterSettings say how to reach the printer.
type PrinterSettings struct {
	Host          string // IP or host name; empty = no printer
	ControlPort   int    // 0 = the protocol's default
	DiscoveryPort int
}

// Settings stores what the user configured in the app.
type Settings interface {
	// PrinterSettings returns the saved printer settings; ok is false when
	// none were saved.
	PrinterSettings(ctx context.Context) (s PrinterSettings, ok bool, err error)
	SavePrinterSettings(ctx context.Context, s PrinterSettings) error
}

// ErrNoPrinter is returned when no printer is configured.
var ErrNoPrinter = errors.New("no printer configured")

// Printing sends library files to the printer and relays its controls.
// One transfer at a time; it runs in the background, since uploads over
// WiFi are slow (a large file takes many minutes). The printer comes from
// the saved settings (or Default when none are saved), so changing them
// takes effect right away.
type Printing struct {
	Store    Store
	Files    Files
	Settings Settings
	Default  PrinterSettings // e.g. from the environment
	Connect  func(PrinterSettings) Printer

	mu       sync.Mutex
	transfer *Transfer
	cancel   context.CancelFunc
	current  Printer
	currentS PrinterSettings
	started  map[string]int64 // file on the printer -> part it was started from
}

// forgetStarted returns (and forgets) the part a print of file was started
// from by this app.
func (p *Printing) forgetStarted(file string) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.started[file]
	delete(p.started, file)
	return id, ok
}

// Config returns the settings in effect and where they come from
// ("saved", "default" or "none").
func (p *Printing) Config(ctx context.Context) (PrinterSettings, string, error) {
	if p.Settings != nil {
		s, ok, err := p.Settings.PrinterSettings(ctx)
		if err != nil {
			return PrinterSettings{}, "", err
		}
		if ok {
			return s, "saved", nil
		}
	}
	if p.Default.Host != "" {
		return p.Default, "default", nil
	}
	return PrinterSettings{}, "none", nil
}

// printer returns the printer for the settings in effect.
func (p *Printing) printer(ctx context.Context) (Printer, error) {
	s, _, err := p.Config(ctx)
	if err != nil {
		return nil, err
	}
	if s.Host == "" {
		return nil, ErrNoPrinter
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil || p.currentS != s {
		p.current, p.currentS = p.Connect(s), s
	}
	return p.current, nil
}

// SaveConfig validates and saves printer settings (an empty host turns
// the printer features off).
func (p *Printing) SaveConfig(ctx context.Context, s PrinterSettings) error {
	s.Host = strings.TrimSpace(s.Host)
	if !validPrinterSettings(s) {
		return ErrInvalid
	}
	return p.Settings.SavePrinterSettings(ctx, s)
}

// TestConfig asks a printer for its status with the given settings,
// without saving them.
func (p *Printing) TestConfig(ctx context.Context, s PrinterSettings) (printer.Status, error) {
	s.Host = strings.TrimSpace(s.Host)
	if s.Host == "" || !validPrinterSettings(s) {
		return printer.Status{}, ErrInvalid
	}
	return p.Connect(s).Status(ctx)
}

func validPrinterSettings(s PrinterSettings) bool {
	okPort := func(n int) bool { return n >= 0 && n <= 65535 }
	return okPort(s.ControlPort) && okPort(s.DiscoveryPort) && len(s.Host) <= 253 &&
		!strings.ContainsAny(s.Host, " /\\?#@")
}

// Status is the printer's state and the current or last transfer.
func (p *Printing) Status(ctx context.Context) (printer.Status, *Transfer, error) {
	pr, err := p.printer(ctx)
	if err != nil {
		return printer.Status{}, nil, err
	}
	st, err := pr.Status(ctx)
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
	pr, err := p.printer(ctx)
	if err != nil {
		return nil, err
	}
	st, err := pr.Status(ctx)
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
	go p.run(bg, pr, *part, t)
	c := *t
	return &c, nil
}

func (p *Printing) run(ctx context.Context, pr Printer, part FileRef, t *Transfer) {
	open := func() (io.ReadCloser, error) { return p.Files.Open(ctx, part.Path) }
	err := pr.Upload(ctx, t.File, part.Size, open, func(sent int64) {
		p.mu.Lock()
		t.Sent = sent
		p.mu.Unlock()
	})
	if err == nil && t.Start {
		err = pr.Start(ctx, t.File)
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
		if p.started == nil {
			p.started = map[string]int64{}
		}
		p.started[t.File] = t.PartID
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
	pr, err := p.printer(ctx)
	if err != nil {
		return err
	}
	switch action {
	case "pause":
		return pr.Pause(ctx)
	case "resume":
		return pr.Resume(ctx)
	case "stop":
		return pr.Stop(ctx)
	}
	return ErrInvalid
}

// PrinterFiles lists a folder of the printer's storage.
func (p *Printing) PrinterFiles(ctx context.Context, dir string) ([]printer.File, error) {
	pr, err := p.printer(ctx)
	if err != nil {
		return nil, err
	}
	return pr.Files(ctx, dir)
}

// PrintExisting starts a file that is already on the printer.
func (p *Printing) PrintExisting(ctx context.Context, name string) error {
	if !printer.Printable(name) {
		return fmt.Errorf("%w: %s is not a sliced printer file (.ctb or .goo)", ErrInvalid, name)
	}
	pr, err := p.printer(ctx)
	if err != nil {
		return err
	}
	return pr.Start(ctx, name)
}

// DeletePrinterFiles removes files from the printer's storage.
func (p *Printing) DeletePrinterFiles(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return ErrInvalid
	}
	pr, err := p.printer(ctx)
	if err != nil {
		return err
	}
	return pr.Delete(ctx, names)
}
