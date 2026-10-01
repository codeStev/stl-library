package app

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// EventDiskLow: a disk the library lives on is nearly full.
const EventDiskLow = "disk.low"

// DiskTarget is a folder whose disk is watched.
type DiskTarget struct {
	Name string // "Library", "Downloads", ...
	Path string
}

// DiskInfo is how much room a watched disk has.
type DiskInfo struct {
	Name       string
	FreeBytes  uint64
	TotalBytes uint64
	Low        bool
	Error      string
}

// DiskMonitor watches the free space of the disks behind some folders and sends EventDiskLow
// when one runs low (again every Repeat while it stays low, not for each check).
type DiskMonitor struct {
	Targets []DiskTarget
	// A disk is low when less than MinFreeBytes or less than MinFreePercent of it is free.
	MinFreeBytes   uint64
	MinFreePercent float64
	Free           func(path string) (free, total uint64, err error)
	Meta           MetaStore
	Notes          notifier
	Repeat         time.Duration // a day when zero
	Now            func() time.Time
}

// Info reads the free space of every target. A nil monitor has nothing to report.
func (m *DiskMonitor) Info() []DiskInfo {
	if m == nil {
		return nil
	}
	var out []DiskInfo
	for _, t := range m.Targets {
		d := DiskInfo{Name: t.Name}
		free, total, err := m.Free(t.Path)
		if err != nil {
			d.Error = err.Error()
		} else {
			d.FreeBytes, d.TotalBytes = free, total
			d.Low = free < m.MinFreeBytes || total > 0 && float64(free)*100/float64(total) < m.MinFreePercent
		}
		out = append(out, d)
	}
	return out
}

// Tick checks the disks and sends a notification for each one that is low and wasn't reported
// lately. A disk that has room again is forgotten, so the next shortage is reported at once.
func (m *DiskMonitor) Tick(ctx context.Context) error {
	if m == nil {
		return nil
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	repeat := m.Repeat
	if repeat <= 0 {
		repeat = 24 * time.Hour
	}
	for _, d := range m.Info() {
		key := "disk_alert_" + d.Name
		v, _, err := m.Meta.Meta(ctx, key)
		if err != nil {
			return err
		}
		last, _ := strconv.ParseInt(v, 10, 64)
		switch {
		case d.Error != "":
			continue
		case !d.Low:
			if last != 0 {
				if err := m.Meta.SetMeta(ctx, key, "0"); err != nil {
					return err
				}
			}
		case last == 0 || now().Sub(time.Unix(last, 0)) >= repeat:
			m.Notes.Notify(ctx, Notification{Event: EventDiskLow, Title: "Disk space is low", Priority: "high",
				Message: fmt.Sprintf("%s: %s of %s free (%.0f%%).", d.Name, GB(d.FreeBytes), GB(d.TotalBytes), float64(d.FreeBytes)*100/float64(max(d.TotalBytes, 1)))})
			if err := m.Meta.SetMeta(ctx, key, strconv.FormatInt(now().Unix(), 10)); err != nil {
				return err
			}
		}
	}
	return nil
}

// GB formats a size in gigabytes.
func GB(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }
