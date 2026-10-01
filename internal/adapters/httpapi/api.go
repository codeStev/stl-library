// Package httpapi is the JSON API over HTTP, a driving adapter: it turns
// requests into app use case calls.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	convention "github.com/codeStev/stl-convention"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/slicemeta"
)

// API serves the library index and its files.
type API struct {
	Store  app.Store
	Files  app.Files
	Thumbs *app.Thumbs
	User   app.UserData
	// Plates stores uploaded sliced files (outside the library).
	Plates app.PlateFiles
	// Previews holds the pictures chosen as a model's preview (from the 3D viewer).
	Previews app.PreviewOverrides
	// Health hashes the files in the background (duplicate finder, integrity check).
	Health *app.Health
	// Editor renames folders (fix suggestions); nil: suggestions are shown but cannot be applied.
	Editor app.LibraryEditor
	// Tidy finds leftovers (junk files, empty folders, identical copies); nil: the feature is off.
	Tidy       *app.Tidy
	review     *app.PreviewReview
	reviewOnce sync.Once
	// Backup says when the library was last backed up (nil: not configured).
	Backup *app.BackupStatus
	// Linker merges duplicate files into hard links; nil when the library can't be changed.
	Linker app.Linker
	// Importer is set when importing from a downloads folder is on.
	Importer *app.Importer
	// Printing handles the (optional) network printer.
	Printing *app.Printing
	// Notifications sends print and import events (ntfy, email).
	Notifications *app.Notifications
	// Scan is the background library scan (status, "rescan now").
	Scan *app.ScanStatus
	// Auth signs users in; without it every request is let through
	// (tests only).
	Auth *app.Auth
	// SecureCookies marks the session cookie Secure even when the request
	// doesn't look like HTTPS (e.g. a proxy that sets no
	// X-Forwarded-Proto).
	SecureCookies bool
	// TrustProxy takes the client address from X-Forwarded-For (only
	// behind a reverse proxy that sets it).
	TrustProxy bool

	limits *limiter
}

// Handler routes /api/… requests (and the Google sign-in redirects under
// /oauth2/ and /login/oauth2/). Everything but signing in needs a full
// login; settings, imports on request and the account roster need an
// admin.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	if a.limits == nil {
		a.limits = newLimiter(10, 30*time.Second)
	}
	h := func(pattern string, lvl level, f http.HandlerFunc) { mux.HandleFunc(pattern, a.guard(lvl, f)) }
	a.authRoutes(h)
	h("GET /api/models", full, a.searchModels)
	h("GET /api/models/{id}", full, a.model)
	h("GET /api/models/{id}/thumb", full, a.modelThumb)
	h("GET /api/previews/review", full, a.reviewState)
	h("POST /api/previews/review/reset", full, a.reviewReset)
	h("POST /api/previews/review/{id}/skip", full, a.reviewSkip)
	h("DELETE /api/previews/review/{id}", full, a.reviewUndo)
	h("PUT /api/models/{id}/preview", full, a.setPreview)
	h("DELETE /api/models/{id}/preview", full, a.resetPreview)
	h("GET /api/creators", full, a.creators)
	h("GET /api/facets", full, a.facets)
	h("GET /api/issues", full, a.issues)
	h("GET /api/variants/{id}/zip", full, a.variantZip)
	h("GET /api/parts/{id}", full, a.part)
	h("GET /api/images/{id}", full, a.image)
	h("GET /api/images/{id}/thumb", full, a.thumb)
	h("GET /api/tags", full, a.tags)
	h("GET /api/printer", full, a.printerStatus)
	h("POST /api/parts/{id}/print", full, a.sendToPrinter)
	h("POST /api/printer/{action}", full, a.printerControl)
	h("DELETE /api/printer/transfer", full, a.cancelTransfer)
	h("GET /api/printer/files", full, a.printerFiles)
	h("POST /api/printer/files/print", full, a.printExisting)
	h("POST /api/printer/files/delete", full, a.deletePrinterFiles)
	h("GET /api/settings/printer", admin, a.printerSettings)
	h("PUT /api/settings/printer", admin, a.savePrinterSettings)
	h("POST /api/settings/printer/test", admin, a.testPrinterSettings)
	h("GET /api/settings/notifications", admin, a.notificationSettings)
	h("PUT /api/settings/notifications", admin, a.saveNotificationSettings)
	h("POST /api/settings/notifications/test", admin, a.testNotifications)
	h("GET /api/fixes", full, a.fixes)
	h("POST /api/fixes/apply", admin, a.applyFix)
	h("POST /api/fixes/{id}/undo", admin, a.undoFix)
	h("GET /api/health", full, a.health)
	h("POST /api/health/run", admin, a.healthRun)
	h("POST /api/health/pause", admin, a.healthPause)
	h("POST /api/health/events/{id}/dismiss", admin, a.dismissHealthEvent)
	h("GET /api/duplicates", full, a.duplicates)
	h("GET /api/set-gaps", full, a.setGaps)
	h("POST /api/duplicates/merge", admin, a.mergeDuplicates)
	h("POST /api/bulk/plan", admin, a.bulkPlan)
	h("POST /api/bulk/apply", admin, a.bulkApply)
	h("GET /api/storage", full, a.storage)
	h("GET /api/tidy", full, a.tidy)
	h("POST /api/tidy/run", admin, a.tidyRun)
	h("POST /api/tidy/apply", admin, a.tidyApply)
	h("GET /api/library/scan", full, a.scanState)
	h("POST /api/library/scan", admin, a.requestScan)
	h("GET /api/imports", full, a.imports)
	h("POST /api/imports/request", admin, a.requestImport)
	h("POST /api/imports/run", admin, a.runImport)
	h("POST /api/imports/retry-failed", admin, a.retryFailedImports)
	h("POST /api/imports/clean-duplicates", admin, a.cleanDuplicateImports)
	h("GET /api/imports/preview", admin, a.previewImport)
	h("GET /api/jobs", full, a.jobs)
	h("POST /api/jobs", full, a.createJob)
	h("GET /api/jobs/{id}", full, a.job)
	h("PUT /api/jobs/{id}", full, a.updateJob)
	h("DELETE /api/jobs/{id}", full, a.deleteJob)
	h("PUT /api/jobs/{id}/items", full, a.setJobItems)
	h("PUT /api/jobs/{id}/plates", full, a.setJobPlates)
	h("POST /api/plates", full, a.uploadPlate)
	h("GET /api/plates/{uid}", full, a.downloadPlate)
	h("GET /api/plates/{uid}/contents", full, a.uploadContents)
	h("PUT /api/plates/{uid}/contents", full, a.setUploadContents)
	h("GET /api/parts/{id}/slicemeta", full, a.partSliceMeta)
	h("GET /api/parts/{id}/preview.png", full, a.partSlicePreview)
	h("GET /api/plates/{uid}/slicemeta", full, a.uploadSliceMeta)
	h("GET /api/plates/{uid}/preview.png", full, a.uploadSlicePreview)
	h("GET /api/parts/{id}/contents", full, a.sliceContents)
	h("GET /api/parts/{id}/contents/suggest", full, a.suggestSliceContents)
	h("PUT /api/parts/{id}/contents", full, a.setSliceContents)
	h("GET /api/variants/{id}/slices", full, a.variantSlices)
	h("GET /api/collections", full, a.collections)
	h("POST /api/collections", full, a.createCollection)
	h("PUT /api/collections/{id}", full, a.updateCollection)
	h("DELETE /api/collections/{id}", full, a.deleteCollection)
	h("POST /api/collections/{id}/models", full, a.editCollection)
	h("GET /api/models/{id}/collections", full, a.modelCollections)
	h("PUT /api/models/{id}/tags", full, a.setTags)
	h("POST /api/models/tags", full, a.editTags)
	h("PUT /api/models/{id}/name", full, a.setName)
	h("PUT /api/models/{id}/hidden", full, a.setHidden)
	h("PUT /api/variants/{id}/label", full, a.setLabel)
	h("DELETE /api/variants/{id}/label", full, a.resetLabel)
	h("POST /api/variants/{id}/prints", full, a.addPrint)
	h("DELETE /api/prints/{id}", full, a.deletePrint)
	h("GET /api/queue", full, a.queue)
	h("PUT /api/variants/{id}/queue", full, a.enqueue)
	h("DELETE /api/variants/{id}/queue", full, a.dequeue)
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
	// Preview: /api/models/{id}/thumb has something to show (an image or
	// a rendered STL).
	Preview     bool     `json:"preview"`
	DisplayName string   `json:"displayName,omitempty"`
	Tags        []string `json:"tags"`
	Prints      int      `json:"prints"`
	Hidden      bool     `json:"hidden,omitempty"`
	// AddedUnix: when the scan first saw the model; LastPrintedUnix: its last print, if any.
	// PreviewVersion: when a chosen preview was made (0: none); part of the picture's URL.
	PreviewVersion  int64 `json:"previewVersion,omitempty"`
	AddedUnix       int64 `json:"addedUnix,omitempty"`
	LastPrintedUnix int64 `json:"lastPrintedUnix,omitempty"`
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
	Prints []print           `json:"prints"`
	Queued bool              `json:"queued"`
	// PrintedParts: how many copies of each part (by id) were printed, from the printed prints.
	PrintedParts map[string]int `json:"printedParts"`
	// Relabeled: dims/option come from a correction, not the folders.
	Relabeled bool `json:"relabeled,omitempty"`
}

type print struct {
	ID   int64  `json:"id"`
	At   int64  `json:"at"` // unix seconds
	Note string `json:"note,omitempty"`
}

func prints(ps []app.Print) []print {
	out := make([]print, 0, len(ps))
	for _, p := range ps {
		out = append(out, print{p.ID, p.AtUnix, p.Note})
	}
	return out
}

type modelDetail struct {
	modelSummary
	Variants []variant `json:"variants"`
	Images   []fileRef `json:"images"`
}

func summary(m app.ModelSummary) modelSummary {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return modelSummary{m.ID, m.Creator, m.Release, m.Category, m.Name, m.Dir, m.Variants, m.Parts, m.Bytes, m.Cover,
		m.Cover != 0 || m.Renderable, m.DisplayName, tags, m.Prints, m.Hidden, 0, m.FirstSeen, m.LastPrinted}
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
	query := app.Query{Text: q.Get("q"), Creator: q.Get("creator"), Tag: q.Get("tag"), Collection: queryID(q.Get("collection")),
		Scale: q.Get("scale"), Supports: q.Get("supports"), Format: q.Get("format"), Fill: q.Get("fill"),
		HasPlate: q.Get("plate") == "yes", AddedDays: int(queryID(q.Get("added"))), Sort: sortParam(q.Get("sort")), Hidden: q.Get("hidden") == "yes",
		Limit: limit, Offset: offset}
	switch q.Get("printed") {
	case "yes":
		t := true
		query.Printed = &t
	case "no":
		f := false
		query.Printed = &f
	}
	hits, err := app.Search(r.Context(), a.Store, query)
	if err != nil {
		fail(w, err)
		return
	}
	total, err := a.Store.CountModels(r.Context(), query)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	out := make([]modelSummary, 0, len(hits))
	for _, h := range hits {
		sm := summary(h)
		sm.PreviewVersion = a.previews().Version(h.Dir)
		out = append(out, sm)
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
	out.PreviewVersion = a.previews().Version(m.Dir)
	for _, v := range m.Variants {
		printed, err := a.Store.PrintedParts(r.Context(), v.ID)
		if err != nil {
			fail(w, err)
			return
		}
		pp := map[string]int{}
		for id, n := range printed {
			pp[strconv.FormatInt(id, 10)] = n
		}
		if len(v.Prints) > 0 { // "Mark printed" on the variant: all its parts were printed
			for _, part := range v.Parts {
				k := strconv.FormatInt(part.ID, 10)
				if pp[k] == 0 {
					pp[k] = 1
				}
			}
		}
		out.Variants = append(out.Variants, variant{
			ID: v.ID, Label: strings.Join(convention.CanonicalSegments(v.Dims), " · "),
			Dims: dims(v.Dims), Option: v.Option, Parts: refs(v.Parts),
			Prints: prints(v.Prints), Queued: v.Queued, Relabeled: v.Relabeled, PrintedParts: pp,
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

// facets lists the values of the variant filters with their model counts.
func (a *API) facets(w http.ResponseWriter, r *http.Request) {
	fs, err := a.Store.Facets(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type facet struct {
		Value  string `json:"value"`
		Models int    `json:"models"`
	}
	out := map[string][]facet{"scale": {}, "supports": {}, "format": {}, "fill": {}}
	for dim, list := range fs {
		for _, f := range list {
			out[dim] = append(out[dim], facet{f.Value, f.Models})
		}
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

func (a *API) thumb(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	data, err := a.Thumbs.Image(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

func (a *API) previews() app.Previews {
	rs, _ := a.Store.(app.ReviewStore)
	return app.Previews{Store: a.Store, Overrides: a.Previews, Review: rs}
}

// reviewUC is the batch review of preview pictures (nil when the store cannot keep it).
func (a *API) reviewUC() *app.PreviewReview {
	rs, ok := a.Store.(app.ReviewStore)
	if !ok {
		return nil
	}
	a.reviewOnce.Do(func() { a.review = &app.PreviewReview{Store: a.Store, Reviews: rs, Overrides: a.Previews} })
	return a.review
}

// setPreview takes a PNG (the viewer's current view) as the model's preview.
func (a *API) setPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20+1))
	if err != nil {
		http.Error(w, "the picture is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := a.previews().Set(r.Context(), id, data); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewState is what is left of the batch review: counts and the next models to show.
func (a *API) reviewState(w http.ResponseWriter, r *http.Request) {
	rv := a.reviewUC()
	if rv == nil {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	st, err := rv.State(r.Context(), r.URL.Query().Get("creator"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"remaining": st.Remaining, "done": st.Done, "next": st.Next})
}

func (a *API) reviewSkip(w http.ResponseWriter, r *http.Request) {
	rv := a.reviewUC()
	id, ok := pathID(w, r)
	if !ok || rv == nil {
		return
	}
	if err := rv.Skip(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reviewUndo(w http.ResponseWriter, r *http.Request) {
	rv := a.reviewUC()
	id, ok := pathID(w, r)
	if !ok || rv == nil {
		return
	}
	if err := rv.Undo(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reviewReset(w http.ResponseWriter, r *http.Request) {
	rv := a.reviewUC()
	if rv == nil {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	n, err := rv.Reset(r.Context(), r.URL.Query().Get("creator"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int{"reset": n})
}

func (a *API) resetPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.previews().Reset(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) modelThumb(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	data, ct, err := a.Thumbs.Model(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

func (a *API) tags(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Store.Tags(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type tag struct {
		Tag    string `json:"tag"`
		Models int    `json:"models"`
	}
	out := make([]tag, 0, len(ts))
	for _, t := range ts {
		out = append(out, tag{t.Tag, t.Models})
	}
	writeJSON(w, out)
}

func (a *API) setTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Tags []string `json:"tags"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	tags, err := a.User.SetTags(r.Context(), id, body.Tags)
	if err != nil {
		fail(w, err)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, map[string][]string{"tags": tags})
}

type partRef struct {
	UploadID  string `json:"uploadId,omitempty"`
	PartID    int64  `json:"partId,omitempty"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	ModelID   int64  `json:"modelId,omitempty"`
	ModelName string `json:"modelName,omitempty"`
	Count     int    `json:"count"`
	Missing   bool   `json:"missing,omitempty"`
}

func partRefs(in []app.PartRef) []partRef {
	out := make([]partRef, 0, len(in))
	for _, r := range in {
		out = append(out, partRef{r.UploadID, r.PartID, r.Path, path.Base(r.Path), r.ModelID, r.ModelName, r.Count, r.Missing})
	}
	return out
}

// sliceContents returns what a sliced file contains and where a part is used.
func (a *API) sliceContents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	info, err := app.Slices{Store: a.Store}.Contents(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"contents": partRefs(info.Contents), "usedIn": partRefs(info.UsedIn)})
}

// suggestSliceContents proposes the parts of the model a plate probably holds, judging by file names.
func (a *API) suggestSliceContents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	refs, err := app.Slices{Store: a.Store}.SuggestContents(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"suggestions": partRefs(refs)})
}

// setSliceContents replaces what a sliced file contains.
func (a *API) setSliceContents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Items []struct {
			PartID int64 `json:"partId"`
			Count  int   `json:"count"`
		} `json:"items"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	items := make([]app.SliceItem, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, app.SliceItem{PartID: it.PartID, Count: it.Count})
	}
	if err := (app.Slices{Store: a.Store}).SetContents(r.Context(), id, items); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// variantSlices lists the sliced files each part of a variant is in.
func (a *API) variantSlices(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cov, err := app.Slices{Store: a.Store}.VariantCoverage(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string][]partRef{}
	for partID, refs := range cov {
		out[strconv.FormatInt(partID, 10)] = partRefs(refs)
	}
	writeJSON(w, map[string]any{"parts": out})
}

type jobSummaryJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Items       int    `json:"items"`
	Copies      int    `json:"copies"`
	Plates      int    `json:"plates"`
	CreatedUnix int64  `json:"createdUnix"`
	PrintedUnix int64  `json:"printedUnix,omitempty"`
}

type jobJSON struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Note        string         `json:"note"`
	State       string         `json:"state"`
	CreatedUnix int64          `json:"createdUnix"`
	PrintedUnix int64          `json:"printedUnix,omitempty"`
	Items       []jobItemJSON  `json:"items"`
	Plates      []jobPlateJSON `json:"plates"`
}

type jobItemJSON struct {
	PartID    int64  `json:"partId,omitempty"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	ModelID   int64  `json:"modelId,omitempty"`
	ModelName string `json:"modelName,omitempty"`
	Count     int    `json:"count"`
	Missing   bool   `json:"missing,omitempty"`
}

type jobPlateJSON struct {
	PartID   int64  `json:"partId,omitempty"`
	UploadID string `json:"uploadId,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
	Missing  bool   `json:"missing,omitempty"`
}

func (a *API) jobsUC() app.Jobs { return app.Jobs{Store: a.Store, Files: a.Plates} }

func (a *API) jobs(w http.ResponseWriter, r *http.Request) {
	js, err := a.jobsUC().List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]jobSummaryJSON, 0, len(js))
	for _, j := range js {
		out = append(out, jobSummaryJSON{j.ID, j.Name, j.State, j.Items, j.Copies, j.Plates, j.CreatedUnix, j.PrintedUnix})
	}
	writeJSON(w, out)
}

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Note string }
	if !readJSON(w, r, &body) {
		return
	}
	id, err := a.jobsUC().Create(r.Context(), body.Name, body.Note)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int64{"id": id})
}

func (a *API) job(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := a.jobsUC().Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	out := jobJSON{ID: j.ID, Name: j.Name, Note: j.Note, State: j.State, CreatedUnix: j.CreatedUnix, PrintedUnix: j.PrintedUnix,
		Items: []jobItemJSON{}, Plates: []jobPlateJSON{}}
	for _, it := range j.Items {
		out.Items = append(out.Items, jobItemJSON{it.PartID, it.Path, path.Base(it.Path), it.ModelID, it.ModelName, it.Count, it.Missing})
	}
	for _, p := range j.Plates {
		out.Plates = append(out.Plates, jobPlateJSON{p.PartID, p.UploadID, p.Name, p.Size, p.Missing})
	}
	writeJSON(w, out)
}

func (a *API) updateJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct{ Name, Note, State string }
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := a.jobsUC().Update(r.Context(), id, body.Name, body.Note, body.State); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.jobsUC().Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setJobItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Items []struct {
			PartID int64 `json:"partId"`
			Count  int   `json:"count"`
		} `json:"items"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	items := make([]app.SliceItem, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, app.SliceItem{PartID: it.PartID, Count: it.Count})
	}
	if err := a.jobsUC().SetItems(r.Context(), id, items); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setJobPlates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Plates []struct {
			PartID   int64  `json:"partId"`
			UploadID string `json:"uploadId"`
		} `json:"plates"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	plates := make([]app.PlateRef, 0, len(body.Plates))
	for _, p := range body.Plates {
		plates = append(plates, app.PlateRef{PartID: p.PartID, UploadID: p.UploadID})
	}
	if err := a.jobsUC().SetPlates(r.Context(), id, plates); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadPlate takes a sliced file as multipart/form-data (field "file"),
// streamed to disk, and stores it in the app's data - not in the library.
func (a *API) uploadPlate(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart/form-data", http.StatusBadRequest)
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			http.Error(w, "no file in the upload", http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			continue
		}
		u, err := a.jobsUC().UploadPlate(r.Context(), part.FileName(), part)
		if err != nil {
			if errors.Is(err, app.ErrTooLarge) {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			fail(w, err)
			return
		}
		writeJSON(w, map[string]any{"id": u.ID, "name": u.Name, "size": u.Size})
		return
	}
}

func (a *API) downloadPlate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	u, err := a.Store.Upload(r.Context(), uid)
	if err != nil {
		fail(w, err)
		return
	}
	f, _, err := a.Plates.Open(r.Context(), uid)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": u.Name}))
	http.ServeContent(w, r, u.Name, time.Time{}, f)
}

func (a *API) uploadContents(w http.ResponseWriter, r *http.Request) {
	refs, err := app.Slices{Store: a.Store}.UploadContents(r.Context(), r.PathValue("uid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"contents": partRefs(refs)})
}

func (a *API) setUploadContents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			PartID int64 `json:"partId"`
			Count  int   `json:"count"`
		} `json:"items"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	items := make([]app.SliceItem, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, app.SliceItem{PartID: it.PartID, Count: it.Count})
	}
	if err := (app.Slices{Store: a.Store}).SetUploadContents(r.Context(), r.PathValue("uid"), items); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sliceMetaJSON struct {
	Format          string  `json:"format"`
	Version         int     `json:"version"`
	Layers          int     `json:"layers"`
	LayerHeight     float32 `json:"layerHeight"`
	ExposureS       float32 `json:"exposureS"`
	BottomExposureS float32 `json:"bottomExposureS"`
	BottomLayers    int     `json:"bottomLayers"`
	PrintSeconds    int     `json:"printSeconds,omitempty"`
	VolumeMl        float32 `json:"volumeMl,omitempty"`
	ResX            int     `json:"resX"`
	ResY            int     `json:"resY"`
	BedX            float32 `json:"bedX,omitempty"`
	BedY            float32 `json:"bedY,omitempty"`
	BedZ            float32 `json:"bedZ,omitempty"`
	HasPreview      bool    `json:"hasPreview"`
}

func (a *API) sliceMeta() app.SliceMeta {
	return app.SliceMeta{Store: a.Store, Files: a.Files, Plates: a.Plates}
}

// metaFail: a file that is not a known sliced format is 422, not a server error.
func metaFail(w http.ResponseWriter, err error) {
	if errors.Is(err, slicemeta.ErrUnsupported) {
		http.Error(w, "not a supported sliced file", http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fail(w, err)
}

func writeSliceMeta(w http.ResponseWriter, m *slicemeta.Meta, err error) {
	if err != nil {
		metaFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, sliceMetaJSON{m.Format, m.Version, m.Layers, m.LayerHeight, m.ExposureS, m.BottomExposureS, m.BottomLayers,
		m.PrintSeconds, m.VolumeMl, m.ResX, m.ResY, m.BedX, m.BedY, m.BedZ, m.HasPreview()})
}

func writeSlicePreview(w http.ResponseWriter, m *slicemeta.Meta, err error) {
	if err != nil {
		metaFail(w, err)
		return
	}
	data, err := m.PreviewPNG()
	if err != nil {
		http.Error(w, "no preview", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

func (a *API) partSliceMeta(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := a.sliceMeta().OfPart(r.Context(), id)
	writeSliceMeta(w, m, err)
}

func (a *API) partSlicePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := a.sliceMeta().OfPart(r.Context(), id)
	writeSlicePreview(w, m, err)
}

func (a *API) uploadSliceMeta(w http.ResponseWriter, r *http.Request) {
	m, err := a.sliceMeta().OfUpload(r.Context(), r.PathValue("uid"))
	writeSliceMeta(w, m, err)
}

func (a *API) uploadSlicePreview(w http.ResponseWriter, r *http.Request) {
	m, err := a.sliceMeta().OfUpload(r.Context(), r.PathValue("uid"))
	writeSlicePreview(w, m, err)
}

func (a *API) fixUC() app.Fixes {
	keys, _ := a.Store.(app.KeyMover)
	return app.Fixes{Store: a.Store, Editor: a.Editor, Keys: keys, Overrides: a.Previews}
}

func (a *API) bulkUC() app.Bulk {
	keys, _ := a.Store.(app.KeyMover)
	dirs, _ := a.Store.(app.ModelDirs)
	return app.Bulk{Store: a.Store, Editor: a.Editor, Keys: keys, Dirs: dirs, Overrides: a.Previews}
}

// fixes lists the suggested folder renames and the journal of applied ones.
func (a *API) fixes(w http.ResponseWriter, r *http.Request) {
	sug, err := a.fixUC().Suggestions(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	journal, err := a.fixUC().Journal(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type suggestion struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Issues int    `json:"issues"`
	}
	type record struct {
		ID     int64  `json:"id"`
		From   string `json:"from"`
		To     string `json:"to"`
		AtUnix int64  `json:"atUnix"`
		Undone bool   `json:"undone,omitempty"`
	}
	out := map[string]any{"canApply": a.Editor != nil, "suggestions": []suggestion{}, "journal": []record{}}
	ss := []suggestion{}
	for _, s := range sug {
		ss = append(ss, suggestion{s.From, s.To, s.Issues})
	}
	rs := []record{}
	for _, j := range journal {
		rs = append(rs, record{j.ID, j.From, j.To, j.AtUnix, j.Undone})
	}
	out["suggestions"], out["journal"] = ss, rs
	writeJSON(w, out)
}

type bulkBody struct {
	IDs      []int64 `json:"ids"`
	Creator  *string `json:"creator"`
	Release  *string `json:"release"`
	Category *string `json:"category"`
	Find     string  `json:"find"`
	Replace  string  `json:"replace"`
}

func (b bulkBody) edit() app.BulkEdit {
	return app.BulkEdit{IDs: b.IDs, Creator: b.Creator, Release: b.Release, Category: b.Category, Find: b.Find, Replace: b.Replace}
}

type bulkRowJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	From    string `json:"from"`
	To      string `json:"to"`
	Problem string `json:"problem,omitempty"`
}

func bulkRows(rows []app.BulkRow) []bulkRowJSON {
	out := make([]bulkRowJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, bulkRowJSON{r.ID, r.Name, r.From, r.To, r.Problem})
	}
	return out
}

// bulkPlan shows what a bulk edit would do with each model, without touching anything.
func (a *API) bulkPlan(w http.ResponseWriter, r *http.Request) {
	var body bulkBody
	if !readJSON(w, r, &body) {
		return
	}
	rows, err := a.bulkUC().Plan(r.Context(), body.edit())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"canApply": a.Editor != nil, "rows": bulkRows(rows)})
}

// bulkApply moves and renames the models the plan shows as free of problems.
func (a *API) bulkApply(w http.ResponseWriter, r *http.Request) {
	var body bulkBody
	if !readJSON(w, r, &body) {
		return
	}
	res, err := a.bulkUC().Apply(r.Context(), body.edit())
	if err != nil {
		fixFail(w, err)
		return
	}
	if res.Done > 0 && a.Scan != nil {
		a.Scan.Request()
	}
	if res.Failed == nil {
		res.Failed = []string{}
	}
	writeJSON(w, map[string]any{"done": res.Done, "failed": res.Failed, "rows": bulkRows(res.Rows)})
}

func (a *API) storage(w http.ResponseWriter, r *http.Request) {
	rep, ok := a.Store.(app.StorageReporter)
	if !ok {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	st, err := rep.Storage(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type release struct {
		Name   string `json:"name"`
		Models int    `json:"models"`
		Bytes  int64  `json:"bytes"`
	}
	type creator struct {
		Name     string    `json:"name"`
		Models   int       `json:"models"`
		Bytes    int64     `json:"bytes"`
		Releases []release `json:"releases"`
	}
	type model struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Creator string `json:"creator"`
		Bytes   int64  `json:"bytes"`
	}
	type kind struct {
		Ext   string `json:"ext"`
		Files int    `json:"files"`
		Bytes int64  `json:"bytes"`
	}
	out := struct {
		Bytes           int64     `json:"bytes"`
		Models          int       `json:"models"`
		Files           int       `json:"files"`
		Creators        []creator `json:"creators"`
		Largest         []model   `json:"largest"`
		Kinds           []kind    `json:"kinds"`
		DuplicateBytes  int64     `json:"duplicateBytes"`
		DuplicateGroups int       `json:"duplicateGroups"`
		Hashed          int       `json:"hashed"`
	}{Bytes: st.Bytes, Models: st.Models, Files: st.Files, Creators: []creator{}, Largest: []model{}, Kinds: []kind{},
		DuplicateBytes: st.DuplicateBytes, DuplicateGroups: st.DuplicateGroups, Hashed: st.Hashed}
	for _, c := range st.Creators {
		jc := creator{Name: c.Name, Models: c.Models, Bytes: c.Bytes, Releases: []release{}}
		for _, rl := range c.Releases {
			jc.Releases = append(jc.Releases, release{rl.Name, rl.Models, rl.Bytes})
		}
		out.Creators = append(out.Creators, jc)
	}
	for _, m := range st.Largest {
		out.Largest = append(out.Largest, model{m.ID, m.Name, m.Creator, m.Bytes})
	}
	for _, k := range st.Kinds {
		out.Kinds = append(out.Kinds, kind{k.Ext, k.Files, k.Bytes})
	}
	writeJSON(w, out)
}

func (a *API) tidy(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		Size     int64  `json:"size,omitempty"`
		Original string `json:"original,omitempty"`
	}
	out := struct {
		Enabled  bool   `json:"enabled"`
		Running  bool   `json:"running"`
		Folders  int    `json:"folders"`
		Finished int64  `json:"finished"`
		Error    string `json:"error,omitempty"`
		Items    []item `json:"items"`
	}{Enabled: a.Tidy != nil, Items: []item{}}
	if a.Tidy != nil {
		st := a.Tidy.State()
		out.Running, out.Folders, out.Finished, out.Error = st.Running, st.Folders, st.FinishedAt, st.Error
		for _, it := range st.Items {
			out.Items = append(out.Items, item{it.Kind, it.Path, it.Size, it.Original})
		}
	}
	writeJSON(w, out)
}

func (a *API) tidyRun(w http.ResponseWriter, r *http.Request) {
	if a.Tidy == nil {
		http.Error(w, "the cleanup is not enabled", http.StatusConflict)
		return
	}
	// the search outlives the request
	a.Tidy.Start(context.WithoutCancel(r.Context()))
	w.WriteHeader(http.StatusAccepted)
}

func (a *API) tidyApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if a.Tidy == nil {
		http.Error(w, "the cleanup is not enabled", http.StatusConflict)
		return
	}
	res, err := a.Tidy.Apply(r.Context(), body.Paths)
	if err != nil {
		fixFail(w, err)
		return
	}
	if res.Done > 0 && a.Scan != nil {
		a.Scan.Request()
	}
	if res.Failed == nil {
		res.Failed = []string{}
	}
	writeJSON(w, map[string]any{"done": res.Done, "failed": res.Failed})
}

func (a *API) applyFix(w http.ResponseWriter, r *http.Request) {
	var body struct{ From, To string }
	if !readJSON(w, r, &body) {
		return
	}
	id, err := a.fixUC().Apply(r.Context(), body.From, body.To)
	if err != nil {
		fixFail(w, err)
		return
	}
	if a.Scan != nil {
		a.Scan.Request() // the library changed: rescan
	}
	writeJSON(w, map[string]int64{"id": id})
}

func (a *API) undoFix(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.fixUC().Undo(r.Context(), id); err != nil {
		fixFail(w, err)
		return
	}
	if a.Scan != nil {
		a.Scan.Request()
	}
	w.WriteHeader(http.StatusNoContent)
}

// fixFail: a folder that is taken or cannot be written is a conflict the user can read, not a server error.
func fixFail(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrExist) || errors.Is(err, os.ErrExist) || strings.Contains(err.Error(), "file exists") {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "the folder is gone", http.StatusConflict)
		return
	}
	fail(w, err)
}

type healthEventJSON struct {
	ID     int64  `json:"id,omitempty"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
	AtUnix int64  `json:"atUnix,omitempty"`
	// OtherCopies: files in the library with the content a damaged file had.
	OtherCopies []string `json:"otherCopies,omitempty"`
}

func healthEvents(es []app.HealthEvent) []healthEventJSON {
	out := make([]healthEventJSON, 0, len(es))
	for _, e := range es {
		out = append(out, healthEventJSON{e.ID, e.Path, e.Detail, e.AtUnix, e.OtherCopies})
	}
	return out
}

// health reports how far the hashing is and what the integrity check found.
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	counts, err := a.Store.HashCounts(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	corrupt, err := a.Store.HealthEvents(r.Context(), "corrupt")
	if err != nil {
		fail(w, err)
		return
	}
	missing, err := a.Store.MissingContent(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	var st app.HealthState
	if a.Health != nil {
		st = a.Health.State()
	}
	bk := a.Backup.Info()
	writeJSON(w, map[string]any{
		"backup":  map[string]any{"configured": bk.Configured, "lastUnix": bk.LastUnix, "overdue": bk.Overdue, "error": bk.Error},
		"enabled": a.Health != nil,
		"files":   counts.Files, "hashed": counts.Hashed,
		"running": st.Running, "paused": st.Paused, "current": st.Current, "done": st.Done, "errors": st.Errors,
		"corrupt": healthEvents(corrupt), "missing": healthEvents(missing),
	})
}

func (a *API) healthRun(w http.ResponseWriter, r *http.Request) {
	if a.Health == nil {
		http.Error(w, "hashing is not enabled", http.StatusConflict)
		return
	}
	a.Health.Resume()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) healthPause(w http.ResponseWriter, r *http.Request) {
	if a.Health == nil {
		http.Error(w, "hashing is not enabled", http.StatusConflict)
		return
	}
	a.Health.Stop()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) dismissHealthEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DismissHealthEvent(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// duplicates lists sets of files with identical content (min: smallest file in KiB, default 64).
func (a *API) duplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	min := int64(64)
	if v, err := strconv.ParseInt(q.Get("min"), 10, 64); err == nil && v >= 0 {
		min = v
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	h := a.Health
	if h == nil {
		h = &app.Health{Store: a.Store}
	}
	groups, total, err := h.Duplicates(r.Context(), min<<10, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	type file struct {
		PartID    int64  `json:"partId"`
		Path      string `json:"path"`
		ModelID   int64  `json:"modelId"`
		ModelName string `json:"modelName"`
		Linked    bool   `json:"linked,omitempty"`
	}
	type group struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
		Wasted int64  `json:"wasted"`
		Files  []file `json:"files"`
	}
	out := make([]group, 0, len(groups))
	var wasted int64
	for _, g := range groups {
		gj := group{SHA256: g.SHA256, Size: g.Size, Wasted: g.Wasted(), Files: []file{}}
		for _, f := range g.Files {
			gj.Files = append(gj.Files, file{f.PartID, f.Path, f.ModelID, f.ModelName, f.Linked})
		}
		wasted += gj.Wasted
		out = append(out, gj)
	}
	writeJSON(w, map[string]any{"total": total, "wastedOnPage": wasted, "canMerge": a.Linker != nil, "groups": out})
}

// setGaps lists models whose supported and unsupported variants (otherwise alike) hold different numbers of files.
func (a *API) setGaps(w http.ResponseWriter, r *http.Request) {
	sr, ok := a.Store.(app.SetReader)
	if !ok {
		http.Error(w, "not available", http.StatusConflict)
		return
	}
	vs, err := sr.SetVariants(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type side struct {
		Label string `json:"label"`
		Files int    `json:"files"`
	}
	type gap struct {
		ModelID   int64    `json:"modelId"`
		ModelName string   `json:"modelName"`
		Sides     []side   `json:"sides"`
		Missing   []string `json:"missing"`
	}
	out := []gap{}
	for _, g := range app.FindSetGaps(vs) {
		gj := gap{ModelID: g.ModelID, ModelName: g.ModelName, Sides: []side{}, Missing: g.Missing}
		if gj.Missing == nil {
			gj.Missing = []string{}
		}
		for _, s := range g.Sides {
			gj.Sides = append(gj.Sides, side{s.Label, s.Files})
		}
		out = append(out, gj)
	}
	writeJSON(w, out)
}

// mergeDuplicates keeps one copy of a duplicate group and turns the others into hard links to it.
func (a *API) mergeDuplicates(w http.ResponseWriter, r *http.Request) {
	ms, ok := a.Store.(app.MergeStore)
	if a.Linker == nil || !ok {
		http.Error(w, "this server cannot change the library", http.StatusConflict)
		return
	}
	var body struct {
		SHA256   string `json:"sha256"`
		Size     int64  `json:"size"`
		KeepPart int64  `json:"keepPart"`
		Remove   bool   `json:"remove"` // delete the other copies instead of linking them
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	n, err := app.MergeDuplicates(r.Context(), ms, a.Linker, body.SHA256, body.Size, body.KeepPart, body.Remove, time.Now())
	if err != nil && n == 0 {
		fail(w, err)
		return
	}
	if n > 0 && body.Remove && a.Scan != nil {
		a.Scan.Request() // the index drops the removed files
	}
	resp := map[string]any{"merged": n}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, resp)
}

// editTags adds and removes tags on many models at once.
func (a *API) editTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []int64  `json:"ids"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	added, removed, err := a.User.EditTags(r.Context(), body.IDs, body.Add, body.Remove)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"models": len(body.IDs), "added": nonNil(added), "removed": nonNil(removed)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *API) setName(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Name string `json:"name"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := a.User.SetDisplayName(r.Context(), id, body.Name); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setHidden(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := a.User.SetHidden(r.Context(), id, body.Hidden); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setLabel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Dims   map[string]string `json:"dims"`
		Option string            `json:"option"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	d := body.Dims
	l := app.VariantLabel{Option: body.Option, Dims: convention.Dims{Scale: d["scale"], Supports: d["supports"],
		Density: d["density"], Format: d["format"], Fill: d["fill"], Split: d["split"], Tech: d["tech"], Extra: d["extra"]}}
	if err := a.User.Relabel(r.Context(), id, l); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) resetLabel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.User.ResetLabel(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addPrint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Note string `json:"note"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	p, err := a.User.MarkPrinted(r.Context(), id, body.Note)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, print{p.ID, p.AtUnix, p.Note})
}

func (a *API) deletePrint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeletePrint(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) queue(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Queue(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	type item struct {
		VariantID int64  `json:"variantId"`
		ModelID   int64  `json:"modelId"`
		Model     string `json:"model"`
		Label     string `json:"label"`
		Added     int64  `json:"added"`
		Note      string `json:"note,omitempty"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		out = append(out, item{it.VariantID, it.ModelID, it.Model, it.Label, it.AddedUnix, it.Note})
	}
	writeJSON(w, out)
}

func (a *API) enqueue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Note string `json:"note"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := a.User.Enqueue(r.Context(), id, body.Note); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) dequeue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.Dequeue(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readJSON decodes a small JSON body. Requiring the JSON content type also
// keeps other web sites from submitting changes through a visitor's
// browser: a cross-site JSON request needs a CORS preflight, which this
// server never grants.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

type printerJob struct {
	File        string  `json:"file"`
	State       string  `json:"state"`
	Layer       int     `json:"layer"`
	Layers      int     `json:"layers"`
	Progress    float64 `json:"progress"`
	ElapsedMs   int64   `json:"elapsedMs"`
	RemainingMs int64   `json:"remainingMs"`
	Error       string  `json:"error,omitempty"`
}

type transfer struct {
	PartID int64  `json:"partId"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	Sent   int64  `json:"sent"`
	Start  bool   `json:"start"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

func transferJSON(t *app.Transfer) *transfer {
	if t == nil {
		return nil
	}
	return &transfer{t.PartID, t.File, t.Size, t.Sent, t.Start, t.State, t.Error}
}

func (a *API) printerStatus(w http.ResponseWriter, r *http.Request) {
	type status struct {
		Name     string      `json:"name"`
		Firmware string      `json:"firmware,omitempty"`
		Machine  string      `json:"machine"`
		UVTemp   float64     `json:"uvTemp,omitempty"`
		Job      *printerJob `json:"job,omitempty"`
	}
	out := struct {
		Enabled  bool      `json:"enabled"`
		Status   *status   `json:"status,omitempty"`
		Transfer *transfer `json:"transfer,omitempty"`
	}{}
	if a.Printing != nil {
		st, t, err := a.Printing.Status(r.Context())
		if errors.Is(err, app.ErrNoPrinter) {
			writeJSON(w, out)
			return
		}
		if err != nil {
			fail(w, err)
			return
		}
		out.Enabled = true
		s := &status{Name: st.Name, Firmware: st.Firmware, Machine: string(st.Machine), UVTemp: st.UVTemp}
		if j := st.Job; j != nil {
			s.Job = &printerJob{j.File, string(j.State), j.Layer, j.Layers, j.Progress(), j.ElapsedMs, j.RemainingMs(), j.Error}
		}
		out.Status, out.Transfer = s, transferJSON(t)
	}
	writeJSON(w, out)
}

func (a *API) sendToPrinter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Start bool `json:"start"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if a.Printing == nil {
		http.Error(w, "no printer configured", http.StatusConflict)
		return
	}
	t, err := a.Printing.Send(r.Context(), id, body.Start)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, transferJSON(t))
}

func (a *API) printerControl(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readJSON(w, r, &body) {
		return
	}
	if a.Printing == nil {
		http.Error(w, "no printer configured", http.StatusConflict)
		return
	}
	if err := a.Printing.Control(r.Context(), r.PathValue("action")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	if a.Printing == nil {
		http.Error(w, "no printer configured", http.StatusConflict)
		return
	}
	if err := a.Printing.CancelTransfer(); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type printerSettingsJSON struct {
	Host          string `json:"host"`
	ControlPort   int    `json:"controlPort,omitempty"`
	DiscoveryPort int    `json:"discoveryPort,omitempty"`
}

func (a *API) printerSettings(w http.ResponseWriter, r *http.Request) {
	if a.Printing == nil {
		writeJSON(w, map[string]any{"source": "none"})
		return
	}
	s, source, err := a.Printing.Config(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, struct {
		printerSettingsJSON
		Source string `json:"source"` // saved, default (environment), none
	}{printerSettingsJSON{s.Host, s.ControlPort, s.DiscoveryPort}, source})
}

func (a *API) savePrinterSettings(w http.ResponseWriter, r *http.Request) {
	var body printerSettingsJSON
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Printing.SaveConfig(r.Context(), app.PrinterSettings{Host: body.Host, ControlPort: body.ControlPort, DiscoveryPort: body.DiscoveryPort}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testPrinterSettings(w http.ResponseWriter, r *http.Request) {
	var body printerSettingsJSON
	if !readJSON(w, r, &body) {
		return
	}
	st, err := a.Printing.TestConfig(r.Context(), app.PrinterSettings{Host: body.Host, ControlPort: body.ControlPort, DiscoveryPort: body.DiscoveryPort})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": st.Machine != "offline", "machine": st.Machine, "name": st.Name, "firmware": st.Firmware})
}

func (a *API) printerFiles(w http.ResponseWriter, r *http.Request) {
	files, err := a.Printing.PrinterFiles(r.Context(), r.URL.Query().Get("dir"))
	if err != nil {
		fail(w, err)
		return
	}
	type file struct {
		Path   string `json:"path"`
		Folder bool   `json:"folder,omitempty"`
		Size   int64  `json:"size,omitempty"`
		Used   int64  `json:"used,omitempty"`
		Total  int64  `json:"total,omitempty"`
	}
	out := make([]file, 0, len(files))
	for _, f := range files {
		out = append(out, file{f.Path, f.Folder, f.Size, f.Used, f.Total})
	}
	writeJSON(w, out)
}

func (a *API) printExisting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Printing.PrintExisting(r.Context(), body.Path); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deletePrinterFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Printing.DeletePrinterFiles(r.Context(), body.Paths); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type notificationSettingsJSON struct {
	NtfyURL       string          `json:"ntfyUrl"`
	NtfyToken     string          `json:"ntfyToken,omitempty"` // write-only
	NtfyTokenSet  bool            `json:"ntfyTokenSet"`
	ClearToken    bool            `json:"clearNtfyToken,omitempty"`
	SMTPHost      string          `json:"smtpHost"`
	SMTPPort      int             `json:"smtpPort,omitempty"`
	SMTPSecurity  string          `json:"smtpSecurity,omitempty"`
	SMTPUser      string          `json:"smtpUsername,omitempty"`
	SMTPPassword  string          `json:"smtpPassword,omitempty"` // write-only
	SMTPPassSet   bool            `json:"smtpPasswordSet"`
	ClearPassword bool            `json:"clearSmtpPassword,omitempty"`
	From          string          `json:"emailFrom,omitempty"`
	To            string          `json:"emailTo,omitempty"`
	Events        map[string]bool `json:"events"`
	AllEvents     []string        `json:"allEvents,omitempty"`
}

func (b notificationSettingsJSON) settings() app.NotificationSettings {
	return app.NotificationSettings{
		Ntfy:   app.NtfySettings{URL: strings.TrimSpace(b.NtfyURL), Token: b.NtfyToken},
		Email:  app.EmailSettings{Host: strings.TrimSpace(b.SMTPHost), Port: b.SMTPPort, Security: b.SMTPSecurity, Username: b.SMTPUser, Password: b.SMTPPassword, From: b.From, To: b.To},
		Events: b.Events,
	}
}

func (a *API) notificationSettings(w http.ResponseWriter, r *http.Request) {
	s, err := a.Notifications.Settings(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, notificationSettingsJSON{
		NtfyURL: s.Ntfy.URL, NtfyTokenSet: s.Ntfy.Token != "",
		SMTPHost: s.Email.Host, SMTPPort: s.Email.Port, SMTPSecurity: s.Email.Security, SMTPUser: s.Email.Username,
		SMTPPassSet: s.Email.Password != "", From: s.Email.From, To: s.Email.To, Events: s.Events, AllEvents: app.AllEvents,
	})
}

func (a *API) saveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var body notificationSettingsJSON
	if !readJSON(w, r, &body) {
		return
	}
	s := body.settings()
	if err := a.Notifications.Save(r.Context(), s, true); err != nil {
		fail(w, err)
		return
	}
	if body.ClearToken || body.ClearPassword { // explicit removal of a stored secret
		cur, err := a.Notifications.Settings(r.Context())
		if err == nil {
			if body.ClearToken {
				cur.Ntfy.Token = ""
			}
			if body.ClearPassword {
				cur.Email.Password = ""
			}
			err = a.Notifications.Save(r.Context(), cur, false)
		}
		if err != nil {
			fail(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testNotifications(w http.ResponseWriter, r *http.Request) {
	var body notificationSettingsJSON
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Notifications.Test(r.Context(), body.settings()); err != nil {
		if errors.Is(err, app.ErrInvalid) {
			fail(w, err)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) imports(w http.ResponseWriter, r *http.Request) {
	type record struct {
		Source  string `json:"source"`
		State   string `json:"state"`
		Target  string `json:"target,omitempty"`
		Files   int    `json:"files"`
		Message string `json:"message,omitempty"`
		Updated int64  `json:"updated"`
	}
	out := struct {
		Enabled bool     `json:"enabled"`
		Running bool     `json:"running"`
		Current string   `json:"current,omitempty"`
		Since   int64    `json:"since,omitempty"`
		Done    int      `json:"done"`
		Records []record `json:"records"`
	}{Enabled: a.Importer != nil, Records: []record{}}
	if a.Importer != nil {
		pr := a.Importer.Progress()
		out.Running, out.Current, out.Since, out.Done = pr.Running, pr.Current, pr.SinceUnix, pr.DoneUnits
		recs, err := a.Importer.Log.ImportRecords(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		for _, rec := range recs {
			out.Records = append(out.Records, record{rec.Source, rec.State, rec.Target, rec.Files, rec.Message, rec.UpdatedUnix})
		}
	}
	writeJSON(w, out)
}

func (a *API) runImport(w http.ResponseWriter, r *http.Request) {
	if a.Importer == nil {
		http.Error(w, "importing is not enabled", http.StatusConflict)
		return
	}
	a.Importer.Trigger()
	w.WriteHeader(http.StatusAccepted)
}

func (a *API) retryFailedImports(w http.ResponseWriter, r *http.Request) {
	if a.Importer == nil {
		http.Error(w, "importing is not enabled", http.StatusConflict)
		return
	}
	n, err := a.Importer.RetryFailed(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if n > 0 {
		a.Importer.Trigger()
	}
	writeJSON(w, map[string]int{"requested": n})
}

// cleanDuplicateImports removes the download folders skipped as duplicates.
func (a *API) cleanDuplicateImports(w http.ResponseWriter, r *http.Request) {
	if a.Importer == nil {
		http.Error(w, "importing is not enabled", http.StatusConflict)
		return
	}
	n, err := a.Importer.CleanDuplicates(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int{"removed": n})
}

// previewImport shows where the files of one download folder would go,
// without writing anything.
func (a *API) previewImport(w http.ResponseWriter, r *http.Request) {
	if a.Importer == nil {
		http.Error(w, "importing is not enabled", http.StatusConflict)
		return
	}
	p, err := a.Importer.PreviewUnit(r.Context(), r.URL.Query().Get("source"))
	if err != nil {
		fail(w, err)
		return
	}
	type placement struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	out := struct {
		Target     string      `json:"target,omitempty"`
		Settled    bool        `json:"settled"`
		Why        string      `json:"why,omitempty"`
		Error      string      `json:"error,omitempty"`
		Total      int         `json:"total"`
		Placements []placement `json:"placements"`
	}{Target: p.Target, Settled: p.Settled, Why: p.Why, Total: len(p.Placements), Placements: []placement{}}
	if p.Err != nil {
		out.Error = p.Err.Error()
	}
	for i, pl := range p.Placements {
		if i >= 200 {
			break
		}
		out.Placements = append(out.Placements, placement{pl.File.Rel, pl.Target})
	}
	writeJSON(w, out)
}

func (a *API) requestImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if a.Importer == nil {
		http.Error(w, "importing is not enabled", http.StatusConflict)
		return
	}
	if err := a.Importer.Request(r.Context(), body.Source); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// sortParam lets only the known sort orders through.
func sortParam(s string) string {
	switch s {
	case "added", "printed":
		return s
	}
	return ""
}

func queryID(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	if n < 0 {
		return 0
	}
	return n
}

type collectionJSON struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Note   string `json:"note"`
	Models int    `json:"models"`
}

func collectionsJSON(cs []app.Collection) []collectionJSON {
	out := make([]collectionJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, collectionJSON{c.ID, c.Name, c.Note, c.Models})
	}
	return out
}

func (a *API) collections(w http.ResponseWriter, r *http.Request) {
	cs, err := app.Collections{Store: a.Store}.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, collectionsJSON(cs))
}

func (a *API) createCollection(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Note string }
	if !readJSON(w, r, &body) {
		return
	}
	c, err := app.Collections{Store: a.Store}.Create(r.Context(), body.Name, body.Note)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, collectionsJSON([]app.Collection{c})[0])
}

func (a *API) updateCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct{ Name, Note string }
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := (app.Collections{Store: a.Store}).Update(r.Context(), id, body.Name, body.Note); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := (app.Collections{Store: a.Store}).Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// editCollection adds and removes models of a collection.
func (a *API) editCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Add    []int64 `json:"add"`
		Remove []int64 `json:"remove"`
	}
	if !ok || !readJSON(w, r, &body) {
		return
	}
	if err := (app.Collections{Store: a.Store}).Edit(r.Context(), id, body.Add, body.Remove); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) modelCollections(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cs, err := app.Collections{Store: a.Store}.OfModel(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, collectionsJSON(cs))
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
	if errors.Is(err, app.ErrInvalid) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, app.ErrBusy) || errors.Is(err, app.ErrNoPrinter) || errors.Is(err, app.ErrExists) {
		http.Error(w, err.Error(), http.StatusConflict)
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

type scanStateJSON struct {
	Running   bool   `json:"running"`
	Started   int64  `json:"started,omitempty"`
	Finished  int64  `json:"finished,omitempty"`
	Added     int    `json:"added"`
	Updated   int    `json:"updated"`
	Removed   int    `json:"removed"`
	Unchanged int    `json:"unchanged"`
	Issues    int    `json:"issues"`
	Pruned    int    `json:"pruned"`
	Error     string `json:"error,omitempty"`
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func (a *API) scanState(w http.ResponseWriter, r *http.Request) {
	if a.Scan == nil {
		http.Error(w, "no background scan", http.StatusNotFound)
		return
	}
	st := a.Scan.Get()
	writeJSON(w, scanStateJSON{st.Running, unix(st.Started), unix(st.Finished), st.Stats.Added, st.Stats.Updated,
		st.Stats.Removed, st.Stats.Unchanged, st.Stats.Issues, st.Pruned, st.Error})
}

// requestScan asks for a rescan now; 202 when queued, 409 when one is
// already running or queued.
func (a *API) requestScan(w http.ResponseWriter, r *http.Request) {
	if a.Scan == nil {
		http.Error(w, "no background scan", http.StatusNotFound)
		return
	}
	if !a.Scan.Request() {
		http.Error(w, "a scan is already running or queued", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
