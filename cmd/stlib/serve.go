package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/adapters/httpapi"
	"github.com/codeStev/stl-library/internal/adapters/notify"
	"github.com/codeStev/stl-library/internal/adapters/secrets"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/adapters/web"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/platform/diskfree"
)

// importConfig turns on importing from a downloads folder.
type importConfig struct {
	source string
	settle time.Duration
	every  time.Duration
	delete bool // remove imported folders from the downloads
}

// serve runs the API and rescans the library every interval (and imports
// new downloads, if configured), until SIGINT/SIGTERM.
func serve(ctx context.Context, root, data, listen string, every time.Duration, imp *importConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := os.MkdirAll(data, 0o755); err != nil {
		return err
	}
	store, err := sqlite.Open(filepath.Join(data, "index.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	files := disk.Files{Root: root}
	previews := disk.PreviewStore{Dir: filepath.Join(data, "previews")}
	thumbs := app.NewThumbs(store, files, disk.ThumbCache{Dir: filepath.Join(data, "thumbs")})
	thumbs.Overrides = previews
	keys, err := secrets.Load(os.Getenv("APP_SECRET"), data)
	if err != nil {
		return err
	}
	notifications := &app.Notifications{Store: store, Sealer: keys, Channels: notify.Channels,
		OnError: func(event string, err error) { slog.Warn("notification not delivered", "event", event, "err", err) }}
	printing := &app.Printing{Store: store, Files: files, Settings: store, Connect: newPrinter}
	if addr := os.Getenv("PRINTER_ADDR"); addr != "" {
		printing.Default = parsePrinterAddr(addr)
	}
	auth, err := newAuth(store, keys, notifications)
	if err != nil {
		return err
	}
	trashDays := 7
	if v := os.Getenv("TRASH_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d >= 0 {
			trashDays = d
		} else {
			slog.Warn("TRASH_DAYS is not a number of days, using 7", "value", v)
		}
	}
	linker := disk.LibraryLinker{Root: root, Trash: trashDays > 0}
	var trash app.TrashStore
	if trashDays > 0 {
		trash = disk.LibraryTrash{Root: root}
		go purgeLoop(ctx, trash, time.Duration(trashDays)*24*time.Hour)
	}
	api := &httpapi.API{Store: store, Files: files, Thumbs: thumbs, Plates: disk.PlateStore{Dir: filepath.Join(data, "plates")}, Previews: previews, Editor: disk.LibraryWriter{Root: root}, Linker: linker, Trash: trash, TrashDays: trashDays, Tidy: &app.Tidy{Tidier: disk.LibraryTidier{Root: root}}, User: app.UserData{Store: store}, Printing: printing,
		Notifications: notifications, Auth: auth, TrustProxy: os.Getenv("TRUST_PROXY_HEADERS") == "true",
		SecureCookies: strings.HasPrefix(os.Getenv("PUBLIC_URL"), "https://")}
	watcher := &app.PrintWatcher{Printing: printing, Store: store, Notify: notifications}
	go watcher.Run(ctx, 20*time.Second)
	mux := http.NewServeMux()
	apiHandler := api.Handler()
	mux.Handle("/api/", apiHandler)
	mux.Handle("/oauth2/", apiHandler)       // Google sign-in: start
	mux.Handle("/login/oauth2/", apiHandler) // and callback
	mux.Handle("/", web.Handler())
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	// A soft memory ceiling for a small shared server, unless the operator
	// set one.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(150 << 20)
	}

	scanStatus := app.NewScanStatus()
	api.Scan = scanStatus
	health := &app.Health{Store: store, Files: files, Pause: 10 * time.Millisecond}
	if os.Getenv("HEALTH_HASH") == "false" {
		health.Stop() // off until someone starts it on the Health page
	}
	api.Health = health
	go health.Run(ctx)
	var importer *app.Importer
	if imp != nil {
		logArchiveTools(newDownloads(imp.source))
		importer = &app.Importer{Downloads: newDownloads(imp.source), Library: disk.LibraryWriter{Root: root}, Log: store, Known: store, Settle: imp.settle,
			DeleteImported: imp.delete}
		api.Importer = importer
		go importLoop(ctx, importer, notifications, imp.every, scanStatus)
	}
	backup := backupFromEnv()
	api.Backup = backup
	disks := diskMonitorFromEnv(root, data, imp, store, notifications)
	api.Disks = disks
	go diskLoop(ctx, disks)
	digest := &app.Digest{Source: store, Backup: backup, Disks: disks, Meta: store, Notes: notifications}
	if imp != nil {
		digest.Imports = store
	}
	go digestLoop(ctx, digest)
	go scanLoop(ctx, disk.Lister{Root: root}, store, thumbs, every, scanStatus, health)

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", listen, "library", root, "version", version)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// backupFromEnv reads BACKUP_MARKER (a file the backup job touches when done, or the backup folder)
// and BACKUP_MAX_AGE (default 168h, a week); nil when no marker is set.
func backupFromEnv() *app.BackupStatus {
	marker := os.Getenv("BACKUP_MARKER")
	if marker == "" {
		return nil
	}
	maxAge := 7 * 24 * time.Hour
	if v := os.Getenv("BACKUP_MAX_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			maxAge = d
		} else {
			slog.Warn("BACKUP_MAX_AGE is not a duration, using 168h", "value", v)
		}
	}
	return &app.BackupStatus{Marker: marker, MaxAge: maxAge, ModTime: func(p string) (time.Time, error) {
		fi, err := os.Stat(p)
		if err != nil {
			return time.Time{}, err
		}
		return fi.ModTime(), nil
	}}
}

// diskMonitorFromEnv watches the disks of the library, the downloads and the data folder:
// DISK_MIN_FREE_GB (default 5) and DISK_MIN_FREE_PERCENT (default 5) say when one is low.
func diskMonitorFromEnv(root, data string, imp *importConfig, meta app.MetaStore, notes *app.Notifications) *app.DiskMonitor {
	minGB, minPct := 5.0, 5.0
	if v := os.Getenv("DISK_MIN_FREE_GB"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			minGB = f
		} else {
			slog.Warn("DISK_MIN_FREE_GB is not a number, using 5", "value", v)
		}
	}
	if v := os.Getenv("DISK_MIN_FREE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f < 100 {
			minPct = f
		} else {
			slog.Warn("DISK_MIN_FREE_PERCENT is not a percentage, using 5", "value", v)
		}
	}
	targets := []app.DiskTarget{{Name: "Library", Path: root}, {Name: "Data", Path: data}}
	if imp != nil {
		targets = append(targets, app.DiskTarget{Name: "Downloads", Path: imp.source})
	}
	return &app.DiskMonitor{Targets: targets, MinFreeBytes: uint64(minGB * (1 << 30)), MinFreePercent: minPct,
		Free: diskfree.Free, Meta: meta, Notes: notes}
}

// diskLoop checks the free space every 15 minutes.
func diskLoop(ctx context.Context, m *app.DiskMonitor) {
	for {
		if err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("disk space check", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Minute):
		}
	}
}

// purgeLoop empties what has been in the trash longer than keep, once an hour.
func purgeLoop(ctx context.Context, t app.TrashStore, keep time.Duration) {
	for {
		if n, err := t.Purge(ctx, time.Now().Add(-keep)); err != nil && ctx.Err() == nil {
			slog.Warn("emptying the trash", "err", err)
		} else if n > 0 {
			slog.Info("trash emptied", "files", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// digestLoop checks every hour whether the weekly digest is due.
func digestLoop(ctx context.Context, d *app.Digest) {
	for {
		if _, err := d.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("weekly digest", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// importLoop imports new downloads every interval; when something was
// imported it asks for a rescan.
func importLoop(ctx context.Context, im *app.Importer, notes *app.Notifications, every time.Duration, scans *app.ScanStatus) {
	for {
		start := time.Now()
		sum, err := im.Run(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			slog.Error("import failed", "err", err)
		case sum.Imported+sum.Failed+sum.Baselined+sum.Duplicates > 0 || sum.Files > 0:
			slog.Info("import done", "took", time.Since(start).Round(time.Second), "imported", sum.Imported, "files", sum.Files, "removed", sum.Removed,
				"skipped as duplicates", sum.Duplicates, "waiting", sum.Waiting, "failed", sum.Failed, "recorded as already there", sum.Baselined)
		}
		if sum.Imported > 0 {
			notes.Notify(ctx, app.Notification{Event: app.EventImportDone, Title: "New models imported",
				Message: fmt.Sprintf("%d download folder(s) imported (%d files).", sum.Imported, sum.Files)})
		}
		if sum.Failed > 0 {
			notes.Notify(ctx, app.Notification{Event: app.EventImportFailed, Title: "Import needs attention", Priority: "high",
				Message: fmt.Sprintf("%d download folder(s) could not be imported - see the Imports page.", sum.Failed)})
		}
		if sum.Files > 0 {
			scans.Request()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-im.Triggered():
		}
	}
}

// scanLoop rescans the library every interval, or when asked (an admin's
// "Rescan now", a finished import); after a successful scan it drops stale
// thumbnails and makes the missing ones.
func scanLoop(ctx context.Context, l app.Lister, s app.Store, thumbs *app.Thumbs, every time.Duration, status *app.ScanStatus, health *app.Health) {
	for {
		start := time.Now()
		status.Start(start)
		st, err := app.Scan(ctx, l, s)
		pruned := 0
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("scan failed", "err", err)
		} else {
			slog.Info("scan done", "took", time.Since(start).Round(time.Millisecond), "added", st.Added,
				"updated", st.Updated, "removed", st.Removed, "unchanged", st.Unchanged, "issues", st.Issues)
			var perr error
			if pruned, perr = thumbs.Prune(ctx); perr != nil {
				slog.Warn("removing stale thumbnails", "err", perr)
			} else if pruned > 0 {
				slog.Info("removed stale thumbnails", "count", pruned)
			}
		}
		status.Done(time.Now(), st, err, pruned)
		if err == nil {
			health.Wake() // new and changed files to hash
			start = time.Now()
			made, failed := thumbs.WarmCovers(ctx)
			if made+failed > 0 {
				slog.Info("cover thumbnails", "made", made, "failed", failed, "took", time.Since(start).Round(time.Second))
			}
			start = time.Now()
			made, failed = thumbs.WarmRenders(ctx)
			if made+failed > 0 {
				slog.Info("model renders", "made", made, "failed", failed, "took", time.Since(start).Round(time.Second))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-status.Wakeup():
		}
	}
}
