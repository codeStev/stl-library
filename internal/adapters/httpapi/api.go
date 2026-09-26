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
	"time"

	convention "github.com/codeStev/stl-convention"
	"github.com/codeStev/stl-library/internal/app"
)

// API serves the library index and its files.
type API struct {
	Store  app.Store
	Files  app.Files
	Thumbs *app.Thumbs
	User   app.UserData
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
	h("GET /api/creators", full, a.creators)
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
	h("GET /api/library/scan", full, a.scanState)
	h("POST /api/library/scan", admin, a.requestScan)
	h("GET /api/imports", full, a.imports)
	h("POST /api/imports/request", admin, a.requestImport)
	h("PUT /api/models/{id}/tags", full, a.setTags)
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
		m.Cover != 0 || m.Renderable, m.DisplayName, tags, m.Prints, m.Hidden}
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
	query := app.Query{Text: q.Get("q"), Creator: q.Get("creator"), Tag: q.Get("tag"), Hidden: q.Get("hidden") == "yes",
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
			Prints: prints(v.Prints), Queued: v.Queued, Relabeled: v.Relabeled,
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
		Records []record `json:"records"`
	}{Enabled: a.Importer != nil, Records: []record{}}
	if a.Importer != nil {
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
	if errors.Is(err, app.ErrBusy) || errors.Is(err, app.ErrNoPrinter) {
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
