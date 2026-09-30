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
	api := &httpapi.API{Store: store, Files: files, Thumbs: thumbs, Plates: disk.PlateStore{Dir: filepath.Join(data, "plates")}, Previews: previews, Editor: disk.LibraryWriter{Root: root}, User: app.UserData{Store: store}, Printing: printing,
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
		importer = &app.Importer{Downloads: newDownloads(imp.source), Library: disk.LibraryWriter{Root: root}, Log: store, Settle: imp.settle,
			DeleteImported: imp.delete}
		api.Importer = importer
		go importLoop(ctx, importer, notifications, imp.every, scanStatus)
	}
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
		case sum.Imported+sum.Failed+sum.Baselined > 0 || sum.Files > 0:
			slog.Info("import done", "took", time.Since(start).Round(time.Second), "imported", sum.Imported, "files", sum.Files, "removed", sum.Removed,
				"waiting", sum.Waiting, "failed", sum.Failed, "recorded as already there", sum.Baselined)
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
