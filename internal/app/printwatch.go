package app

import (
	"context"
	"fmt"
	"time"

	"github.com/codeStev/stl-library/internal/core/printer"
)

// PrintWatcher follows the printer: when a print finishes, stops or runs
// into an error it sends a notification, and a print that was started
// from the app is recorded in that variant's print history when it
// completes.
type PrintWatcher struct {
	Printing *Printing
	Store    Store
	Notify   *Notifications
	Now      func() time.Time

	last *printer.Job
}

// Check looks at the printer once and acts on what changed since the
// previous check.
func (w *PrintWatcher) Check(ctx context.Context) {
	st, _, err := w.Printing.Status(ctx)
	if err != nil || st.Machine == printer.Offline {
		return // no printer, or unreachable: nothing to compare
	}
	prev := w.last
	w.last = st.Job
	j := st.Job
	if j == nil || prev == nil {
		return // first look: only remember
	}
	sameJob := prev.File == j.File
	switch {
	case sameJob && prev.Active() && j.State == printer.JobComplete:
		w.Notify.Notify(ctx, Notification{Event: EventPrintDone, Title: "Print finished",
			Message: fmt.Sprintf("%s is done (%d layers).", j.File, j.Layers)})
		w.record(ctx, j)
	case sameJob && prev.Active() && j.State == printer.JobStopped:
		w.Notify.Notify(ctx, Notification{Event: EventPrintStopped, Title: "Print stopped",
			Message: fmt.Sprintf("%s was stopped at layer %d of %d.", j.File, j.Layer, j.Layers)})
		w.Printing.forgetStarted(j.File)
	}
	if j.Error != "" && (prev.Error != j.Error || !sameJob) {
		w.Notify.Notify(ctx, Notification{Event: EventPrintError, Title: "Printer error", Priority: "high",
			Message: fmt.Sprintf("%s: %s (layer %d of %d).", j.File, j.Error, j.Layer, j.Layers)})
	}
}

// record adds a print to the history of the variant the job was started
// from - only for prints the app started, since only then is the variant
// known.
func (w *PrintWatcher) record(ctx context.Context, j *printer.Job) {
	partID, ok := w.Printing.forgetStarted(j.File)
	if !ok {
		return
	}
	variantID, err := w.Store.PartVariant(ctx, partID)
	if err != nil {
		return
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	w.Store.AddPrint(ctx, variantID, now().Unix(), "printed from the app")
}

// Run checks every interval until ctx is done.
func (w *PrintWatcher) Run(ctx context.Context, every time.Duration) {
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
