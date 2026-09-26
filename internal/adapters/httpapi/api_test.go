package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/codeStev/stl-library/internal/adapters/disk"
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
