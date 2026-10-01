package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDiskMonitorReportsALowDiskOnceADayAndAgainAfterItRecovered(t *testing.T) {
	now := time.Unix(5_000_000, 0)
	free := uint64(50 << 30)
	notes := &fakeNotes{}
	m := &DiskMonitor{
		Targets:      []DiskTarget{{Name: "Library", Path: "/lib"}},
		MinFreeBytes: 10 << 30, MinFreePercent: 5,
		Free: func(string) (uint64, uint64, error) { return free, 1000 << 30, nil },
		Meta: fakeMeta{}, Notes: notes, Now: func() time.Time { return now },
	}
	ctx := context.Background()
	check := func(want int) {
		t.Helper()
		if err := m.Tick(ctx); err != nil || len(notes.sent) != want {
			t.Fatalf("sent %d, want %d (%v)", len(notes.sent), want, err)
		}
	}
	check(0)
	free = 8 << 30 // under the byte limit
	check(1)
	if n := notes.sent[0]; n.Event != EventDiskLow || !strings.Contains(n.Message, "Library") || n.Priority != "high" {
		t.Errorf("notification %+v", n)
	}
	now = now.Add(time.Hour)
	check(1) // still low, reported lately
	now = now.Add(24 * time.Hour)
	check(2) // a day later
	free = 200 << 30
	check(2) // recovered
	free = 20 << 30
	total := uint64(1000 << 30)
	m.Free = func(string) (uint64, uint64, error) { return free, total, nil }
	free = 20 << 30 // 2% of 1000 GB: under the percentage
	check(3)        // reported at once after the recovery
	if i := m.Info(); len(i) != 1 || !i[0].Low {
		t.Errorf("info %+v", i)
	}
	if (*DiskMonitor)(nil).Info() != nil || (*DiskMonitor)(nil).Tick(ctx) != nil {
		t.Error("nil monitor")
	}
}
