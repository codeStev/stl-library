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
	thumbs := app.NewThumbs(store, files, disk.ThumbCache{Dir: filepath.Join(data, "thumbs")})
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
	api := &httpapi.API{Store: store, Files: files, Thumbs: thumbs, User: app.UserData{Store: store}, Printing: printing,
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

	rescan := make(chan struct{}, 1)
	var importer *app.Importer
	if imp != nil {
		importer = &app.Importer{Downloads: disk.Downloads{Root: imp.source}, Library: disk.LibraryWriter{Root: root}, Log: store, Settle: imp.settle}
		api.Importer = importer
		go importLoop(ctx, importer, notifications, imp.every, rescan)
	}
	go scanLoop(ctx, disk.Lister{Root: root}, store, thumbs, every, rescan)

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
func importLoop(ctx context.Context, im *app.Importer, notes *app.Notifications, every time.Duration, rescan chan<- struct{}) {
	for {
		start := time.Now()
		sum, err := im.Run(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			slog.Error("import failed", "err", err)
		case sum.Imported+sum.Failed+sum.Baselined > 0 || sum.Files > 0:
			slog.Info("import done", "took", time.Since(start).Round(time.Second), "imported", sum.Imported, "files", sum.Files,
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
			select {
			case rescan <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func scanLoop(ctx context.Context, l app.Lister, s app.Store, thumbs *app.Thumbs, every time.Duration, rescan <-chan struct{}) {
	for {
		start := time.Now()
		st, err := app.Scan(ctx, l, s)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("scan failed", "err", err)
		} else {
			slog.Info("scan done", "took", time.Since(start).Round(time.Millisecond), "added", st.Added,
				"updated", st.Updated, "removed", st.Removed, "unchanged", st.Unchanged, "issues", st.Issues)
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
		case <-rescan:
		}
	}
}
