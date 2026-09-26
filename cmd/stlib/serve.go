package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/adapters/httpapi"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/adapters/web"
	"github.com/codeStev/stl-library/internal/app"
)

// serve runs the API and rescans the library every interval, until
// SIGINT/SIGTERM.
func serve(ctx context.Context, root, data, listen string, every time.Duration) error {
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
	api := &httpapi.API{Store: store, Files: files, Thumbs: thumbs, User: app.UserData{Store: store}}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", web.Handler())
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	// A soft memory ceiling for a small shared server, unless the operator
	// set one.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(150 << 20)
	}

	go scanLoop(ctx, disk.Lister{Root: root}, store, thumbs, every)

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

func scanLoop(ctx context.Context, l app.Lister, s app.Store, thumbs *app.Thumbs, every time.Duration) {
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
		}
	}
}
