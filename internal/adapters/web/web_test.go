package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServesTheUIOrAHint(t *testing.T) {
	root, _ := fs.Sub(dist, "dist")
	_, err := fs.Stat(root, "index.html")
	built := err == nil
	for _, p := range []string{"/", "/model/12"} {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		switch {
		case built && (rec.Code != 200 || !strings.Contains(rec.Body.String(), `<div id="root">`)):
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		case !built && (rec.Code != 404 || !strings.Contains(rec.Body.String(), "npm run build")):
			t.Errorf("%s without UI: %d %q", p, rec.Code, rec.Body.String())
		}
	}
}
