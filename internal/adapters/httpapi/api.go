// Package httpapi is the JSON API over HTTP, a driving adapter: it turns
// requests into app use case calls.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/codeStev/stl-library/convention"
	"github.com/codeStev/stl-library/internal/app"
)

// API serves the library index and its files.
type API struct {
	Store app.Store
	Files app.Files
}

// Handler routes /api/… requests.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models", a.searchModels)
	mux.HandleFunc("GET /api/models/{id}", a.model)
	mux.HandleFunc("GET /api/creators", a.creators)
	mux.HandleFunc("GET /api/issues", a.issues)
	mux.HandleFunc("GET /api/variants/{id}/zip", a.variantZip)
	mux.HandleFunc("GET /api/parts/{id}", a.part)
	mux.HandleFunc("GET /api/images/{id}", a.image)
	return mux
}

type modelSummary struct {
	ID       int64  `json:"id"`
	Creator  string `json:"creator"`
	Release  string `json:"release,omitempty"`
	Category string `json:"category,omitempty"`
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Variants int    `json:"variants"`
	Parts    int    `json:"parts"`
	Bytes    int64  `json:"bytes"`
	Cover    int64  `json:"cover,omitempty"` // image id
}

type fileRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type variant struct {
	ID     int64             `json:"id"`
	Label  string            `json:"label"` // "32mm · Supported Lychee · Hollow"
	Dims   map[string]string `json:"dims"`
	Option string            `json:"option,omitempty"`
	Parts  []fileRef         `json:"parts"`
}

type modelDetail struct {
	modelSummary
	Variants []variant `json:"variants"`
	Images   []fileRef `json:"images"`
}

func summary(m app.ModelSummary) modelSummary {
	return modelSummary{m.ID, m.Creator, m.Release, m.Category, m.Name, m.Dir, m.Variants, m.Parts, m.Bytes, m.Cover}
}

func refs(fs []app.FileRef) []fileRef {
	out := make([]fileRef, 0, len(fs))
	for _, f := range fs {
		out = append(out, fileRef{f.ID, path.Base(f.Path), f.Size})
	}
	return out
}

func dims(d convention.Dims) map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{"scale": d.Scale, "supports": d.Supports, "density": d.Density, "format": d.Format,
		"fill": d.Fill, "split": d.Split, "tech": d.Tech, "extra": d.Extra} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func (a *API) searchModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	hits, err := app.Search(r.Context(), a.Store, app.Query{Text: q.Get("q"), Creator: q.Get("creator"), Limit: limit, Offset: offset})
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]modelSummary, 0, len(hits))
	for _, h := range hits {
		out = append(out, summary(h))
	}
	writeJSON(w, out)
}

func (a *API) model(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := a.Store.Model(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	out := modelDetail{modelSummary: summary(m.ModelSummary), Variants: []variant{}, Images: refs(m.Images)}
	for _, v := range m.Variants {
		out.Variants = append(out.Variants, variant{
			ID: v.ID, Label: strings.Join(convention.CanonicalSegments(v.Dims), " · "),
			Dims: dims(v.Dims), Option: v.Option, Parts: refs(v.Parts),
		})
	}
	writeJSON(w, out)
}

func (a *API) creators(w http.ResponseWriter, r *http.Request) {
	cs, err := a.Store.Creators(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type creator struct {
		Name   string `json:"name"`
		Models int    `json:"models"`
	}
	out := make([]creator, 0, len(cs))
	for _, c := range cs {
		out = append(out, creator{c.Name, c.Models})
	}
	writeJSON(w, out)
}

func (a *API) issues(w http.ResponseWriter, r *http.Request) {
	is, err := a.Store.Issues(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type issue struct {
		Dir    string `json:"dir"`
		Reason string `json:"reason"`
	}
	out := make([]issue, 0, len(is))
	for _, i := range is {
		out = append(out, issue{i.Dir, i.Reason})
	}
	writeJSON(w, out)
}

func (a *API) variantZip(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	started := false
	err := app.VariantZip(r.Context(), a.Store, a.Files, id, func(name string) {
		started = true
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", attachment(name))
	}, w)
	if err != nil {
		if !started {
			fail(w, err)
			return
		}
		// Headers are out; the client sees a truncated download.
		slog.Warn("zip download aborted", "variant", id, "err", err)
	}
}

func (a *API) part(w http.ResponseWriter, r *http.Request) {
	a.serveFile(w, r, func(id int64) (*app.FileRef, error) { return a.Store.Part(r.Context(), id) }, true)
}

func (a *API) image(w http.ResponseWriter, r *http.Request) {
	a.serveFile(w, r, func(id int64) (*app.FileRef, error) { return a.Store.Image(r.Context(), id) }, false)
}

func (a *API) serveFile(w http.ResponseWriter, r *http.Request, lookup func(int64) (*app.FileRef, error), download bool) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, err := lookup(id)
	if err != nil {
		fail(w, err)
		return
	}
	rc, err := a.Files.Open(r.Context(), f.Path)
	if err != nil {
		fail(w, err)
		return
	}
	defer rc.Close()
	ct := mime.TypeByExtension(path.Ext(f.Path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if download {
		w.Header().Set("Content-Disposition", attachment(path.Base(f.Path)))
	}
	io.Copy(w, rc)
}

// attachment builds a Content-Disposition header that survives non-ASCII
// names (RFC 6266 / RFC 5987).
func attachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return `attachment; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(name)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.Error("request failed", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing response", "err", err)
	}
}
