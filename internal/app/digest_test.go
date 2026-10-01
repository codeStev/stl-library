package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeMeta map[string]string

func (m fakeMeta) Meta(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}
func (m fakeMeta) SetMeta(_ context.Context, k, v string) error { m[k] = v; return nil }

type fakeSource struct{ corrupt, missing int }

func (fakeSource) CountModels(context.Context, Query) (int, error) { return 4, nil }
func (f fakeSource) HealthEvents(context.Context, string) ([]HealthEvent, error) {
	return make([]HealthEvent, f.corrupt), nil
}
func (f fakeSource) MissingContent(context.Context) ([]HealthEvent, error) {
	return make([]HealthEvent, f.missing), nil
}

type fakeNotes struct{ sent []Notification }

func (f *fakeNotes) Notify(_ context.Context, n Notification) { f.sent = append(f.sent, n) }

func TestDigestStartsItsClockThenSendsAWeeklySummary(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	notes := &fakeNotes{}
	log := &memLog{recs: map[string]ImportRecord{
		"a": {Source: "a", State: ImportDone, UpdatedUnix: now.Unix() + 3600*24*8 - 100},
		"b": {Source: "b", State: ImportDuplicate, UpdatedUnix: now.Unix() + 3600*24*8 - 100},
		"c": {Source: "c", State: ImportDone, UpdatedUnix: now.Unix() - 100}, // before the week
		"d": {Source: "d", State: ImportFailed},
	}}
	backup := &BackupStatus{Marker: "m", MaxAge: 24 * time.Hour, Now: func() time.Time { return now },
		ModTime: func(string) (time.Time, error) { return now.Add(-72 * time.Hour), nil }}
	d := &Digest{Source: fakeSource{corrupt: 1}, Imports: log, Backup: backup, Meta: fakeMeta{}, Notes: notes, Now: func() time.Time { return now }}
	ctx := context.Background()
	if sent, err := d.Tick(ctx); sent || err != nil {
		t.Fatalf("first tick: %v %v", sent, err)
	}
	now = now.Add(3 * 24 * time.Hour)
	if sent, _ := d.Tick(ctx); sent {
		t.Fatal("sent before a week")
	}
	now = now.Add(5 * 24 * time.Hour)
	sent, err := d.Tick(ctx)
	if !sent || err != nil || len(notes.sent) != 1 {
		t.Fatalf("second: %v %v %v", sent, err, notes.sent)
	}
	n := notes.sent[0]
	for _, want := range []string{"New models in the library: 4", "imported: 1, skipped as duplicates: 1, needing attention: 1", "Damaged files: 1", "OVERDUE"} {
		if !strings.Contains(n.Message, want) {
			t.Errorf("missing %q in\n%s", want, n.Message)
		}
	}
	if n.Event != EventDigest || n.Priority != "high" {
		t.Errorf("notification %+v", n)
	}
	if sent, _ := d.Tick(ctx); sent {
		t.Error("sent twice")
	}
}

func TestBackupInfo(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	b := &BackupStatus{Marker: "m", MaxAge: time.Hour, Now: func() time.Time { return now }}
	if (*BackupStatus)(nil).Info().Configured {
		t.Error("nil is configured")
	}
	b.ModTime = func(string) (time.Time, error) { return now.Add(-30 * time.Minute), nil }
	if i := b.Info(); !i.Configured || i.Overdue || i.LastUnix == 0 {
		t.Errorf("fresh: %+v", i)
	}
	b.ModTime = func(string) (time.Time, error) { return now.Add(-2 * time.Hour), nil }
	if i := b.Info(); !i.Overdue {
		t.Errorf("old: %+v", i)
	}
	b.ModTime = func(string) (time.Time, error) { return time.Time{}, errors.New("gone") }
	if i := b.Info(); !i.Overdue || i.Error == "" {
		t.Errorf("unreadable: %+v", i)
	}
}
