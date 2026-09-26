package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeStev/stl-library/internal/core/printer"
)

type fakePrinter struct {
	mu       sync.Mutex
	machine  printer.Machine
	uploaded map[string]string
	started  []string
	block    chan struct{} // uploads wait on it when set
	actions  []string
}

func (f *fakePrinter) Status(context.Context) (printer.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return printer.Status{Name: "Saturn 4 Ultra", Machine: f.machine}, nil
}

func (f *fakePrinter) Upload(ctx context.Context, name string, size int64, open func() (io.ReadCloser, error), progress func(int64)) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r, err := open()
	if err != nil {
		return err
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	progress(int64(len(b)))
	f.mu.Lock()
	f.uploaded[name] = string(b)
	f.mu.Unlock()
	return nil
}

func (f *fakePrinter) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, name)
	return nil
}
func (f *fakePrinter) Files(context.Context, string) ([]printer.File, error) { return nil, nil }
func (f *fakePrinter) Delete(context.Context, []string) error                { return nil }
func (f *fakePrinter) Pause(context.Context) error {
	f.actions = append(f.actions, "pause")
	return nil
}
func (f *fakePrinter) Resume(context.Context) error {
	f.actions = append(f.actions, "resume")
	return nil
}
func (f *fakePrinter) Stop(context.Context) error { f.actions = append(f.actions, "stop"); return nil }

type partStore struct{ Store }

func (partStore) Part(_ context.Context, id int64) (*FileRef, error) {
	switch id {
	case 1:
		return &FileRef{ID: 1, Path: "Wicked/Panther/Supported Chitubox/panther.ctb", Size: 5}, nil
	case 2:
		return &FileRef{ID: 2, Path: "Wicked/Panther/Supported Chitubox/panther.chitubox", Size: 5}, nil
	}
	return nil, ErrNotFound
}

type oneFile struct{}

func (oneFile) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("CTB!!")), nil
}

func wait(t *testing.T, p *Printing, state string) *Transfer {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, tr, _ := p.Status(context.Background()); tr != nil && tr.State == state {
			return tr
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, tr, _ := p.Status(context.Background())
	t.Fatalf("transfer never reached %q: %+v", state, tr)
	return nil
}

func TestSendUploadsAndStartsInTheBackground(t *testing.T) {
	fp := &fakePrinter{machine: printer.Idle, uploaded: map[string]string{}}
	p := &Printing{Printer: fp, Store: partStore{}, Files: oneFile{}}
	ctx := context.Background()
	if _, err := p.Send(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	tr := wait(t, p, "printing")
	if fp.uploaded["panther.ctb"] != "CTB!!" || len(fp.started) != 1 || fp.started[0] != "panther.ctb" || tr.Sent != 5 {
		t.Errorf("uploaded %v started %v transfer %+v", fp.uploaded, fp.started, tr)
	}
	// Only uploading: no start.
	if _, err := p.Send(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	wait(t, p, "done")
	if len(fp.started) != 1 {
		t.Errorf("started without being asked: %v", fp.started)
	}
}

func TestSendRefusesWhatCantBeSent(t *testing.T) {
	fp := &fakePrinter{machine: printer.Idle, uploaded: map[string]string{}, block: make(chan struct{})}
	p := &Printing{Printer: fp, Store: partStore{}, Files: oneFile{}}
	ctx := context.Background()
	if _, err := p.Send(ctx, 2, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("project file: %v", err)
	}
	if _, err := p.Send(ctx, 9, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown part: %v", err)
	}
	if _, err := p.Send(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Send(ctx, 1, false); !errors.Is(err, ErrBusy) {
		t.Errorf("second transfer: %v", err)
	}
	if err := p.CancelTransfer(); err != nil {
		t.Fatal(err)
	}
	wait(t, p, "cancelled")
	fp.machine = printer.Printing
	if _, err := p.Send(ctx, 1, false); !errors.Is(err, ErrBusy) {
		t.Errorf("while printing: %v", err)
	}
}

func TestControl(t *testing.T) {
	fp := &fakePrinter{}
	p := &Printing{Printer: fp}
	for _, a := range []string{"pause", "resume", "stop"} {
		if err := p.Control(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(fp.actions, ",") != "pause,resume,stop" {
		t.Errorf("actions %v", fp.actions)
	}
	if err := p.Control(context.Background(), "explode"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown action: %v", err)
	}
}
