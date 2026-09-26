// Package web serves the embedded web UI (built from /web into dist by
// `npm run build`). Unknown paths get index.html so client-side routes work.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the UI, or a hint when the binary was built without it.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web UI not built into this binary (run `npm run build` in web/ before `go build`)", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(root, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					// Vite puts a content hash in asset names.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, root, "index.html")
	})
}
