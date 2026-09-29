package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/adapters/notify"
	"github.com/codeStev/stl-library/internal/adapters/sdcp"
	"github.com/codeStev/stl-library/internal/adapters/sdcp/sdcptest"
	"github.com/codeStev/stl-library/internal/adapters/secrets"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
)

// server builds a small library on disk, scans it and serves the API.
func server(t *testing.T) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	for p, content := range map[string]string{
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported Lychee/bell.lys": "LYS",
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/Supported Lychee/base.lys": "BASE",
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/32mm/No Supports/bell.stl":      "STL",
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/cover.jpg":                      tinyJPEG(),
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/zz-broken.jpg":                  "not a JPEG",
		"Loot Studios/Abyssal Haze/Enemies/Bell Head/render.png":                     tinyPNG(),
		"Artisan Guild/Noble Alfar/Kövön the Wise/k.stl":                             "K",
		"Lord of the Print/Unchained/Araki/Presupported/a.stl":                       "A",
	} {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(content), 0o644)
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := app.Scan(context.Background(), disk.Lister{Root: root}, store); err != nil {
		t.Fatal(err)
	}
	files := disk.Files{Root: root}
	thumbs := app.NewThumbs(store, files, disk.ThumbCache{Dir: t.TempDir()})
	srv := httptest.NewServer((&API{Store: store, Files: files, Thumbs: thumbs, User: app.UserData{Store: store}}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func getJSON(t *testing.T, srv *httptest.Server, path string, v any) {
	t.Helper()
	resp, body := get(t, srv, path)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("%s: %v in %s", path, err, body)
	}
}

func TestSearchThenModelDetail(t *testing.T) {
	srv := server(t)
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=bell", &hits)
	if len(hits) != 1 || hits[0].Name != "Bell Head" || hits[0].Variants != 2 {
		t.Fatalf("hits: %+v", hits)
	}
	var m modelDetail
	getJSON(t, srv, "/api/models/"+itoa(hits[0].ID), &m)
	if len(m.Variants) != 2 || len(m.Images) != 3 || m.Images[0].Name != "cover.jpg" {
		t.Fatalf("detail: %+v", m)
	}
	v := m.Variants[1]
	if v.Label != "32mm · Supported Lychee" || v.Dims["format"] != "Lychee" || len(v.Parts) != 2 {
		t.Errorf("variant: %+v", v)
	}
	var all []modelSummary
	getJSON(t, srv, "/api/models?creator=Artisan+Guild", &all)
	if len(all) != 1 || all[0].Name != "Kövön the Wise" {
		t.Errorf("creator filter: %+v", all)
	}
}

func TestCreatorsAndIssues(t *testing.T) {
	srv := server(t)
	var cs []struct {
		Name   string `json:"name"`
		Models int    `json:"models"`
	}
	getJSON(t, srv, "/api/creators", &cs)
	if len(cs) != 2 {
		t.Errorf("creators: %+v", cs)
	}
	var is []struct{ Dir, Reason string }
	getJSON(t, srv, "/api/issues", &is)
	if len(is) != 1 || !strings.Contains(is[0].Reason, "Presupported") {
		t.Errorf("issues: %+v", is)
	}
}

func TestVariantZipAndFiles(t *testing.T) {
	srv := server(t)
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=bell", &hits)
	var m modelDetail
	getJSON(t, srv, "/api/models/"+itoa(hits[0].ID), &m)
	lychee := m.Variants[1]

	resp, body := get(t, srv, "/api/variants/"+itoa(lychee.ID)+"/zip")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("zip: %d %v", resp.StatusCode, resp.Header)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="Bell Head - 32mm Supported Lychee.zip"`) {
		t.Errorf("disposition %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("zip: %v %d files", err, len(zr.File))
	}

	resp, body = get(t, srv, "/api/images/"+itoa(m.Images[0].ID))
	if resp.StatusCode != 200 || string(body) != tinyJPEG() || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("image: %d %q %v", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	resp, body = get(t, srv, "/api/parts/"+itoa(lychee.Parts[0].ID))
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") || len(body) == 0 {
		t.Errorf("part: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestUnknownAndBadIDs(t *testing.T) {
	srv := server(t)
	for path, want := range map[string]int{
		"/api/models/999":       404,
		"/api/models/abc":       400,
		"/api/models/-1":        400,
		"/api/variants/999/zip": 404,
		"/api/images/999":       404,
		"/api/parts/0":          400,
		"/api/nothing":          404,
	} {
		if resp, _ := get(t, srv, path); resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestAttachmentHeaderSurvivesNonASCII(t *testing.T) {
	got := attachment(`Kövön "the" Wise.zip`)
	want := `attachment; filename="K_v_n _the_ Wise.zip"; filename*=UTF-8''K%C3%B6v%C3%B6n%20%22the%22%20Wise.zip`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func tinyJPEG() string {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 20, 20)), nil)
	return b.String()
}

func tinyPNG() string {
	var b bytes.Buffer
	png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 900, 300)))
	return b.String()
}

func TestImageThumbnail(t *testing.T) {
	srv := server(t)
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=bell", &hits)
	var m modelDetail
	getJSON(t, srv, "/api/models/"+itoa(hits[0].ID), &m)
	resp, body := get(t, srv, "/api/images/"+itoa(m.Images[1].ID)+"/thumb") // render.png
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d %s", resp.StatusCode, body)
	}
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil || img.Bounds().Dx() != 400 || img.Bounds().Dy() != 133 {
		t.Errorf("thumb image: %v %v", err, img.Bounds())
	}
	if resp, _ := get(t, srv, "/api/images/"+itoa(m.Images[2].ID)+"/thumb"); resp.StatusCode != 500 {
		t.Errorf("thumb of a broken image: %d", resp.StatusCode)
	}
	// Models without an image: the Artisan model's k.stl is one byte of
	// text, so rendering fails; the model with only a .lys has no preview.
	var all []modelSummary
	getJSON(t, srv, "/api/models?creator=Artisan+Guild", &all)
	if len(all) != 1 || !all[0].Preview || all[0].Cover != 0 {
		t.Fatalf("artisan: %+v", all)
	}
	if resp, _ := get(t, srv, "/api/models/"+itoa(all[0].ID)+"/thumb"); resp.StatusCode != 500 {
		t.Errorf("render of a broken STL: %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv, "/api/models/"+itoa(hits[0].ID)+"/thumb"); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("model with cover: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func send(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTagsNamesPrintsAndQueue(t *testing.T) {
	srv := server(t)
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=bell", &hits)
	id := itoa(hits[0].ID)

	if code, body := send(t, srv, "PUT", "/api/models/"+id+"/tags", `{"tags":[" painted ","Painted","wip"]}`); code != 200 || body != `{"tags":["painted","wip"]}`+"\n" {
		t.Errorf("set tags: %d %s", code, body)
	}
	if code, _ := send(t, srv, "PUT", "/api/models/"+id+"/name", `{"name":"Bellringer"}`); code != 204 {
		t.Errorf("set name: %d", code)
	}
	var tagged []modelSummary
	getJSON(t, srv, "/api/models?tag=wip", &tagged)
	if len(tagged) != 1 || tagged[0].DisplayName != "Bellringer" || len(tagged[0].Tags) != 2 {
		t.Errorf("by tag: %+v", tagged)
	}

	var m modelDetail
	getJSON(t, srv, "/api/models/"+id, &m)
	vid := itoa(m.Variants[0].ID)
	if code, _ := send(t, srv, "PUT", "/api/variants/"+vid+"/queue", `{"note":"next weekend"}`); code != 204 {
		t.Errorf("enqueue: %d", code)
	}
	var q []struct {
		VariantID int64  `json:"variantId"`
		Model     string `json:"model"`
		Note      string `json:"note"`
	}
	getJSON(t, srv, "/api/queue", &q)
	if len(q) != 1 || q[0].Model != "Bellringer" || q[0].Note != "next weekend" {
		t.Errorf("queue: %+v", q)
	}
	code, body := send(t, srv, "POST", "/api/variants/"+vid+"/prints", `{"note":"looks great"}`)
	if code != 201 || !strings.Contains(body, "looks great") {
		t.Errorf("print: %d %s", code, body)
	}
	getJSON(t, srv, "/api/queue", &q)
	if len(q) != 0 {
		t.Errorf("printing did not take it off the queue: %+v", q)
	}
	var printed []modelSummary
	getJSON(t, srv, "/api/models?printed=yes", &printed)
	if len(printed) != 1 || printed[0].Prints != 1 {
		t.Errorf("printed: %+v", printed)
	}
	getJSON(t, srv, "/api/models/"+id, &m)
	if len(m.Variants[0].Prints) != 1 {
		t.Fatalf("variant prints: %+v", m.Variants[0])
	}
	if code, _ := send(t, srv, "DELETE", "/api/prints/"+itoa(m.Variants[0].Prints[0].ID), ""); code != 204 {
		t.Errorf("delete print: %d", code)
	}
	var tags []struct {
		Tag    string
		Models int
	}
	getJSON(t, srv, "/api/tags", &tags)
	if len(tags) != 2 {
		t.Errorf("tags: %+v", tags)
	}
}

func TestWritesNeedJSON(t *testing.T) {
	srv := server(t)
	req, _ := http.NewRequest("PUT", srv.URL+"/api/models/1/tags", strings.NewReader(`tags=x`))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form post: %d", resp.StatusCode)
	}
	if code, _ := send(t, srv, "PUT", "/api/models/1/name", `{"name":`); code != 400 {
		t.Errorf("broken JSON: %d", code)
	}
	if code, _ := send(t, srv, "PUT", "/api/models/1/name", `{"name":"`+strings.Repeat("x", 201)+`"}`); code != 400 {
		t.Errorf("too long: %d", code)
	}
	if code, _ := send(t, srv, "POST", "/api/variants/999/prints", `{}`); code != 404 {
		t.Errorf("unknown variant: %d", code)
	}
}

func TestHideRelabelAndImportsDisabled(t *testing.T) {
	srv := server(t)
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=bell", &hits)
	id := itoa(hits[0].ID)
	if code, _ := send(t, srv, "PUT", "/api/models/"+id+"/hidden", `{"hidden":true}`); code != 204 {
		t.Fatalf("hide: %d", code)
	}
	getJSON(t, srv, "/api/models?q=bell", &hits)
	if len(hits) != 0 {
		t.Errorf("hidden model listed: %+v", hits)
	}
	getJSON(t, srv, "/api/models?q=bell&hidden=yes", &hits)
	if len(hits) != 1 || !hits[0].Hidden {
		t.Fatalf("with hidden: %+v", hits)
	}
	var m modelDetail
	getJSON(t, srv, "/api/models/"+id, &m)
	vid := itoa(m.Variants[0].ID)
	if code, body := send(t, srv, "PUT", "/api/variants/"+vid+"/label", `{"dims":{"scale":"75mm","supports":"Supported"},"option":"Helmet"}`); code != 204 {
		t.Fatalf("relabel: %d %s", code, body)
	}
	getJSON(t, srv, "/api/models/"+id, &m)
	var v variant
	for _, x := range m.Variants {
		if itoa(x.ID) == vid {
			v = x
		}
	}
	if v.Label != "75mm · Supported" || v.Option != "Helmet" || !v.Relabeled {
		t.Errorf("relabeled variant: %+v", v)
	}
	if code, _ := send(t, srv, "PUT", "/api/variants/"+vid+"/label", `{"dims":{"supports":"presupported"}}`); code != 400 {
		t.Errorf("invalid label: %d", code)
	}
	if code, _ := send(t, srv, "DELETE", "/api/variants/"+vid+"/label", ""); code != 204 {
		t.Errorf("reset: %d", code)
	}
	var imp struct {
		Enabled bool `json:"enabled"`
	}
	getJSON(t, srv, "/api/imports", &imp)
	if imp.Enabled {
		t.Error("import enabled without a source")
	}
	if code, _ := send(t, srv, "POST", "/api/imports/request", `{"source":"x"}`); code != 409 {
		t.Errorf("request without importer: %d", code)
	}
}

func TestPrinterAgainstTheMock(t *testing.T) {
	// A server with a mock printer; never a real one.
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Wicked/Panther/Supported Chitubox"), 0o755)
	os.WriteFile(filepath.Join(root, "Wicked/Panther/Supported Chitubox/panther.ctb"), []byte("CTBDATA"), 0o644)
	os.WriteFile(filepath.Join(root, "Wicked/Panther/Supported Chitubox/panther.chitubox"), []byte("PROJECT"), 0o644)
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	defer store.Close()
	app.Scan(context.Background(), disk.Lister{Root: root}, store)
	mock := sdcptest.New()
	h, u, err := mock.Listen("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	port := func(addr string) int { _, p, _ := net.SplitHostPort(addr); n, _ := strconv.Atoi(p); return n }
	files := disk.Files{Root: root}
	printing := &app.Printing{Store: store, Files: files, Settings: store,
		Connect: func(s app.PrinterSettings) app.Printer {
			return &sdcp.Printer{Host: s.Host, ControlPort: s.ControlPort, DiscoveryPort: s.DiscoveryPort, Timeout: 3 * time.Second}
		}}
	api := &API{Store: store, Files: files, User: app.UserData{Store: store}, Printing: printing}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	// No printer yet: off. Then configure the mock through the settings.
	var off struct {
		Enabled bool `json:"enabled"`
	}
	getJSON(t, srv, "/api/printer", &off)
	if off.Enabled {
		t.Error("printer enabled without settings")
	}
	mockSettings := fmt.Sprintf(`{"host":"127.0.0.1","controlPort":%d,"discoveryPort":%d}`, port(h), port(u))
	if code, body := send(t, srv, "POST", "/api/settings/printer/test", mockSettings); code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("test settings: %d %s", code, body)
	}
	if code, _ := send(t, srv, "PUT", "/api/settings/printer", mockSettings); code != 204 {
		t.Fatalf("save settings: %d", code)
	}
	var cfg struct {
		Host   string `json:"host"`
		Source string `json:"source"`
	}
	getJSON(t, srv, "/api/settings/printer", &cfg)
	if cfg.Host != "127.0.0.1" || cfg.Source != "saved" {
		t.Errorf("settings: %+v", cfg)
	}

	var m modelDetail
	var hits []modelSummary
	getJSON(t, srv, "/api/models?q=panther", &hits)
	getJSON(t, srv, "/api/models/"+itoa(hits[0].ID), &m)
	var ctb, project int64
	for _, p := range m.Variants[0].Parts {
		if strings.HasSuffix(p.Name, ".ctb") {
			ctb = p.ID
		} else {
			project = p.ID
		}
	}
	if code, body := send(t, srv, "POST", "/api/parts/"+itoa(project)+"/print", `{"start":true}`); code != 400 {
		t.Errorf("project file: %d %s", code, body)
	}
	if code, body := send(t, srv, "POST", "/api/parts/"+itoa(ctb)+"/print", `{"start":true}`); code != 202 {
		t.Fatalf("send: %d %s", code, body)
	}
	var st struct {
		Enabled bool `json:"enabled"`
		Status  struct {
			Machine string `json:"machine"`
			Job     *struct {
				File  string `json:"file"`
				State string `json:"state"`
			} `json:"job"`
		} `json:"status"`
		Transfer *struct {
			State string `json:"state"`
			Sent  int64  `json:"sent"`
		} `json:"transfer"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv, "/api/printer", &st)
		if st.Transfer != nil && st.Transfer.State == "printing" && st.Status.Machine == "printing" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.Enabled || st.Transfer == nil || st.Transfer.State != "printing" || st.Transfer.Sent != 7 || st.Status.Job == nil || st.Status.Job.File != "panther.ctb" {
		t.Fatalf("printer status: %+v", st)
	}
	if code, _ := send(t, srv, "POST", "/api/parts/"+itoa(ctb)+"/print", `{"start":false}`); code != 409 {
		t.Errorf("send while printing: %d", code)
	}
	for _, a := range []string{"pause", "resume", "stop"} {
		if code, body := send(t, srv, "POST", "/api/printer/"+a, `{}`); code != 204 {
			t.Errorf("%s: %d %s", a, code, body)
		}
	}
	if code, _ := send(t, srv, "POST", "/api/printer/explode", `{}`); code != 400 {
		t.Errorf("unknown action: %d", code)
	}
	var pf []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	getJSON(t, srv, "/api/printer/files", &pf)
	if len(pf) != 1 || pf[0].Path != "/local/panther.ctb" || pf[0].Size != 7 {
		t.Errorf("printer files: %+v", pf)
	}
	if code, body := send(t, srv, "POST", "/api/printer/files/print", `{"path":"/local/panther.ctb"}`); code != 204 {
		t.Errorf("print existing: %d %s", code, body)
	}
	send(t, srv, "POST", "/api/printer/stop", `{}`)
	if code, _ := send(t, srv, "POST", "/api/printer/files/delete", `{"paths":["/local/panther.ctb"]}`); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if got := strings.Join(mock.Log(), ","); got != "start /local/panther.ctb,pause,resume,stop,start /local/panther.ctb,stop,delete /local/panther.ctb" {
		t.Errorf("mock saw: %s", got)
	}
}

func TestNotificationSettingsNeverReturnSecrets(t *testing.T) {
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	defer store.Close()
	keys, _ := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
	var gotAuth string
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotAuth = r.Header.Get("Authorization") }))
	defer ntfy.Close()
	api := &API{Store: store, Notifications: &app.Notifications{Store: store, Sealer: keys, Channels: notify.Channels}}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	body := `{"ntfyUrl":"` + ntfy.URL + `/printer","ntfyToken":"tk_secret","smtpHost":"","events":{"print.done":true}}`
	if code, b := send(t, srv, "PUT", "/api/settings/notifications", body); code != 204 {
		t.Fatalf("save: %d %s", code, b)
	}
	_, raw := get(t, srv, "/api/settings/notifications")
	if strings.Contains(string(raw), "tk_secret") || !strings.Contains(string(raw), `"ntfyTokenSet":true`) {
		t.Errorf("settings response: %s", raw)
	}
	// Test with the saved token (not sent again).
	if code, b := send(t, srv, "POST", "/api/settings/notifications/test", `{"ntfyUrl":"`+ntfy.URL+`/printer","events":{}}`); code != 204 || gotAuth != "Bearer tk_secret" {
		t.Errorf("test: %d %s auth=%q", code, b, gotAuth)
	}
	if code, _ := send(t, srv, "POST", "/api/settings/notifications/test", `{"ntfyUrl":"http://127.0.0.1:1/x","events":{}}`); code != 502 {
		t.Errorf("unreachable ntfy: %d", code)
	}
	if code, _ := send(t, srv, "PUT", "/api/settings/notifications", `{"ntfyUrl":"nope","events":{}}`); code != 400 {
		t.Errorf("invalid: %d", code)
	}
}

func TestRescanNow(t *testing.T) {
	status := app.NewScanStatus()
	srv := httptest.NewServer((&API{Scan: status}).Handler())
	defer srv.Close()
	post := func() int {
		resp, err := http.Post(srv.URL+"/api/library/scan", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(); code != 202 {
		t.Errorf("first request: %d", code)
	}
	if code := post(); code != 409 {
		t.Errorf("second request while queued: %d", code)
	}
	select {
	case <-status.Wakeup():
	default:
		t.Error("the scan loop was not woken")
	}
	status.Start(time.Unix(100, 0))
	status.Done(time.Unix(105, 0), app.SyncStats{Added: 3, Unchanged: 7}, nil, 2)
	var st scanStateJSON
	getJSON(t, srv, "/api/library/scan", &st)
	if st.Running || st.Finished != 105 || st.Added != 3 || st.Pruned != 2 {
		t.Errorf("state %+v", st)
	}
}

func TestBulkTagEdit(t *testing.T) {
	srv := server(t)
	var all []modelSummary
	getJSON(t, srv, "/api/models", &all)
	if len(all) < 2 {
		t.Fatalf("%d models", len(all))
	}
	ids := fmt.Sprintf("[%d,%d]", all[0].ID, all[1].ID)
	if code, body := send(t, srv, "POST", "/api/models/tags", `{"ids":`+ids+`,"add":[" Table Top ","dnd","DND"],"remove":[]}`); code != 200 ||
		body != `{"added":["Table Top","dnd"],"models":2,"removed":[]}`+"\n" {
		t.Errorf("bulk add: %d %s", code, body)
	}
	var tagged []modelSummary
	getJSON(t, srv, "/api/models?tag=dnd", &tagged)
	if len(tagged) != 2 {
		t.Errorf("tagged %d", len(tagged))
	}
	if code, _ := send(t, srv, "POST", "/api/models/tags", `{"ids":`+ids+`,"remove":["dnd"]}`); code != 200 {
		t.Errorf("bulk remove: %d", code)
	}
	getJSON(t, srv, "/api/models?tag=dnd", &tagged)
	if len(tagged) != 0 {
		t.Errorf("after remove %d", len(tagged))
	}
	// A tag typed in another case takes the spelling of the existing one.
	send(t, srv, "POST", "/api/models/tags", `{"ids":`+ids+`,"add":["Boss Fight"]}`)
	if code, body := send(t, srv, "POST", "/api/models/tags", `{"ids":`+ids+`,"add":["boss fight"]}`); code != 200 || !strings.Contains(body, `"added":["Boss Fight"]`) {
		t.Errorf("canonical spelling: %d %s", code, body)
	}
	var tagList []struct {
		Tag    string `json:"tag"`
		Models int    `json:"models"`
	}
	getJSON(t, srv, "/api/tags", &tagList)
	for _, tg := range tagList {
		if strings.EqualFold(tg.Tag, "boss fight") && tg.Tag != "Boss Fight" {
			t.Errorf("a second spelling appeared: %+v", tagList)
		}
	}
	for _, bad := range []string{`{"ids":[],"add":["x"]}`, `{"ids":[1]}`, `{"ids":[1],"add":[""]}`} {
		if code, _ := send(t, srv, "POST", "/api/models/tags", bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code, _ := send(t, srv, "POST", "/api/models/tags", `{"ids":[99999],"add":["x"]}`); code != 404 {
		t.Errorf("unknown model: %d", code)
	}
}
