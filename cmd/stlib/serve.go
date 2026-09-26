package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

	api := &httpapi.API{Store: store, Files: disk.Files{Root: root}}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", web.Handler())
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go scanLoop(ctx, disk.Lister{Root: root}, store, every)

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", listen, "library", root)
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

func scanLoop(ctx context.Context, l app.Lister, s app.Store, every time.Duration) {
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
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
