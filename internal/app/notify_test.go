package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/codeStev/stl-library/internal/core/printer"
)

type memNotifyStore struct{ s NotificationSettings }

func (m *memNotifyStore) NotificationSettings(context.Context) (NotificationSettings, error) {
	if m.s.Events == nil {
		m.s.Events = map[string]bool{}
	}
	return m.s, nil
}
func (m *memNotifyStore) SaveNotificationSettings(_ context.Context, s NotificationSettings) error {
	m.s = s
	return nil
}

// rot13 stands in for encryption.
type rot struct{}

func (rot) Seal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return "sealed:" + s, nil
}
func (rot) Open(s string) (string, error) { return strings.TrimPrefix(s, "sealed:"), nil }

type captured struct {
	mu   sync.Mutex
	sent []Notification
}

func (c *captured) Send(_ context.Context, n Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, n)
	return nil
}

func newNotifications(c *captured) (*Notifications, *memNotifyStore) {
	st := &memNotifyStore{}
	return &Notifications{Store: st, Sealer: rot{}, Channels: func(s NotificationSettings) []Notifier {
		if s.Ntfy.URL == "" {
			return nil
		}
		return []Notifier{c}
	}}, st
}

func TestNotificationSettingsKeepSecretsSealed(t *testing.T) {
	c := &captured{}
	n, st := newNotifications(c)
	ctx := context.Background()
	s := NotificationSettings{Ntfy: NtfySettings{URL: "https://ntfy.sh/printer", Token: "tk"},
		Email:  EmailSettings{Host: "smtp.example.org", From: "a@example.org", To: "b@example.org", Password: "pw"},
		Events: map[string]bool{EventPrintDone: true}}
	if err := n.Save(ctx, s, true); err != nil {
		t.Fatal(err)
	}
	if st.s.Ntfy.Token != "sealed:tk" || st.s.Email.Password != "sealed:pw" {
		t.Errorf("stored in the clear: %+v", st.s)
	}
	// Saving again without secrets keeps them.
	s.Ntfy.Token, s.Email.Password = "", ""
	n.Save(ctx, s, true)
	if got, _ := n.Settings(ctx); got.Ntfy.Token != "tk" || got.Email.Password != "pw" {
		t.Errorf("secrets lost: %+v", got)
	}
	for _, bad := range []NotificationSettings{
		{Ntfy: NtfySettings{URL: "ftp://x/y"}},
		{Ntfy: NtfySettings{URL: "https://ntfy.sh/"}},
		{Email: EmailSettings{Host: "h", From: "not an address", To: "b@x.org"}},
		{Email: EmailSettings{Host: "h", From: "a@x.org", To: "b@x.org", Security: "ssl3"}},
		{Events: map[string]bool{"print.exploded": true}},
	} {
		if err := n.Save(ctx, bad, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
}

func TestNotifyOnlyEnabledEvents(t *testing.T) {
	c := &captured{}
	n, _ := newNotifications(c)
	ctx := context.Background()
	n.Save(ctx, NotificationSettings{Ntfy: NtfySettings{URL: "https://ntfy.sh/p"}, Events: map[string]bool{EventPrintDone: true}}, false)
	n.Notify(ctx, Notification{Event: EventPrintDone, Title: "done"})
	n.Notify(ctx, Notification{Event: EventImportDone, Title: "imported"})
	if len(c.sent) != 1 || c.sent[0].Title != "done" {
		t.Errorf("sent %+v", c.sent)
	}
	if err := n.Test(ctx, NotificationSettings{Ntfy: NtfySettings{URL: "https://ntfy.sh/p"}}); err != nil || len(c.sent) != 2 {
		t.Errorf("test: %v %+v", err, c.sent)
	}
	if err := n.Test(ctx, NotificationSettings{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("test without channels: %v", err)
	}
	var none *Notifications
	none.Notify(ctx, Notification{Event: EventPrintDone}) // no notifications configured: no panic
}

// scriptedPrinter returns the queued statuses one after another.
type scriptedPrinter struct {
	fakePrinter
	jobs []*printer.Job
}

func (s *scriptedPrinter) Status(context.Context) (printer.Status, error) {
	j := s.jobs[0]
	if len(s.jobs) > 1 {
		s.jobs = s.jobs[1:]
	}
	return printer.Status{Machine: printer.Idle, Job: j}, nil
}

type printStore struct {
	partStore
	prints []int64
}

func (p *printStore) PartVariant(context.Context, int64) (int64, error) { return 42, nil }
func (p *printStore) AddPrint(_ context.Context, variantID, _ int64, _ string) (Print, error) {
	p.prints = append(p.prints, variantID)
	return Print{}, nil
}

func TestWatcherNotifiesAndRecordsPrintsStartedFromTheApp(t *testing.T) {
	c := &captured{}
	n, _ := newNotifications(c)
	ctx := context.Background()
	n.Save(ctx, NotificationSettings{Ntfy: NtfySettings{URL: "https://ntfy.sh/p"},
		Events: map[string]bool{EventPrintDone: true, EventPrintStopped: true, EventPrintError: true}}, false)
	job := func(file string, st printer.JobState, err string) *printer.Job {
		return &printer.Job{File: file, State: st, Layer: 5, Layers: 10, Error: err}
	}
	sp := &scriptedPrinter{jobs: []*printer.Job{
		job("app.ctb", printer.JobPrinting, ""),
		job("app.ctb", printer.JobComplete, ""),   // finished, started from the app: recorded
		job("other.ctb", printer.JobPrinting, ""), // started on the printer itself
		job("other.ctb", printer.JobPrinting, "resin level low"),
		job("other.ctb", printer.JobStopped, "resin level low"),
		job("other.ctb", printer.JobStopped, "resin level low"), // nothing new
	}}
	store := &printStore{}
	p := &Printing{Store: store, Default: PrinterSettings{Host: "mock"}, Connect: func(PrinterSettings) Printer { return sp }}
	p.started = map[string]int64{"app.ctb": 7}
	w := &PrintWatcher{Printing: p, Store: store, Notify: n}
	for i := 0; i < 6; i++ {
		w.Check(ctx)
	}
	var titles []string
	for _, s := range c.sent {
		titles = append(titles, s.Title)
	}
	if strings.Join(titles, ",") != "Print finished,Printer error,Print stopped" {
		t.Errorf("notifications: %v", titles)
	}
	if len(store.prints) != 1 || store.prints[0] != 42 {
		t.Errorf("recorded prints: %v", store.prints)
	}
}
