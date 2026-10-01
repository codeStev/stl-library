package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EventDigest is the weekly summary.
const EventDigest = "digest.weekly"

// MetaStore keeps small values by key.
type MetaStore interface {
	Meta(ctx context.Context, key string) (value string, ok bool, err error)
	SetMeta(ctx context.Context, key, value string) error
}

// DigestSource is what the digest reads from the index.
type DigestSource interface {
	CountModels(ctx context.Context, q Query) (int, error)
	HealthEvents(ctx context.Context, kind string) ([]HealthEvent, error)
	MissingContent(ctx context.Context) ([]HealthEvent, error)
}

type notifier interface {
	Notify(ctx context.Context, n Notification)
}

// Digest sends one summary a week (if the event is switched on in the notification settings):
// new models, what the importer did, damaged or lost files, and the backup.
type Digest struct {
	Source  DigestSource
	Imports ImportLog // nil when importing is off
	Backup  *BackupStatus
	Disks   *DiskMonitor
	Meta    MetaStore
	Notes   notifier
	Every   time.Duration // 7 days when zero
	Now     func() time.Time
}

const digestKey = "digest_last_unix"

func (d *Digest) every() time.Duration {
	if d.Every > 0 {
		return d.Every
	}
	return 7 * 24 * time.Hour
}

func (d *Digest) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Tick sends the digest when a week has passed since the last one. The first call only starts
// the clock, so a new installation doesn't send one at once. It reports whether one was sent.
func (d *Digest) Tick(ctx context.Context) (bool, error) {
	now := d.now()
	v, ok, err := d.Meta.Meta(ctx, digestKey)
	if err != nil {
		return false, err
	}
	last, _ := strconv.ParseInt(v, 10, 64)
	if !ok || last <= 0 {
		return false, d.Meta.SetMeta(ctx, digestKey, strconv.FormatInt(now.Unix(), 10))
	}
	since := time.Unix(last, 0)
	if now.Sub(since) < d.every() {
		return false, nil
	}
	note, err := d.build(ctx, since, now)
	if err != nil {
		return false, err
	}
	d.Notes.Notify(ctx, note)
	return true, d.Meta.SetMeta(ctx, digestKey, strconv.FormatInt(now.Unix(), 10))
}

func (d *Digest) build(ctx context.Context, since, now time.Time) (Notification, error) {
	var lines []string
	days := int(now.Sub(since).Hours()/24 + 0.5)
	if days < 1 {
		days = 1
	}
	added, err := d.Source.CountModels(ctx, Query{AddedDays: days})
	if err != nil {
		return Notification{}, err
	}
	lines = append(lines, fmt.Sprintf("New models in the library: %d", added))
	if d.Imports != nil {
		recs, err := d.Imports.ImportRecords(ctx)
		if err != nil {
			return Notification{}, err
		}
		var imported, skipped, failed int
		for _, r := range recs {
			if r.UpdatedUnix < since.Unix() {
				continue
			}
			switch r.State {
			case ImportDone:
				imported++
			case ImportDuplicate:
				skipped++
			}
		}
		for _, r := range recs {
			if r.State == ImportFailed {
				failed++ // all that wait, however old
			}
		}
		lines = append(lines, fmt.Sprintf("Download folders imported: %d, skipped as duplicates: %d, needing attention: %d", imported, skipped, failed))
	}
	corrupt, err := d.Source.HealthEvents(ctx, "corrupt")
	if err != nil {
		return Notification{}, err
	}
	missing, err := d.Source.MissingContent(ctx)
	if err != nil {
		return Notification{}, err
	}
	lines = append(lines, fmt.Sprintf("Damaged files: %d, files that are gone: %d", len(corrupt), len(missing)))
	priority := "default"
	if len(corrupt)+len(missing) > 0 {
		priority = "high"
	}
	for _, disk := range d.Disks.Info() {
		switch {
		case disk.Error != "":
			lines = append(lines, "Disk "+disk.Name+": can't be read: "+disk.Error)
		case disk.Low:
			lines = append(lines, "Disk "+disk.Name+": LOW - only "+GB(disk.FreeBytes)+" free")
			priority = "high"
		default:
			lines = append(lines, "Disk "+disk.Name+": "+GB(disk.FreeBytes)+" free")
		}
	}
	if b := d.Backup.Info(); b.Configured {
		switch {
		case b.Error != "":
			lines = append(lines, "Backup: "+b.Error)
			priority = "high"
		case b.Overdue:
			lines = append(lines, "Backup: OVERDUE - the last one was "+time.Unix(b.LastUnix, 0).Format("2006-01-02"))
			priority = "high"
		default:
			lines = append(lines, "Backup: last one "+time.Unix(b.LastUnix, 0).Format("2006-01-02"))
		}
	}
	return Notification{Event: EventDigest, Title: "STL Library - the week", Message: strings.Join(lines, "\n"), Priority: priority}, nil
}
