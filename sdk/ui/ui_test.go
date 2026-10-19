package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesOnlyItsFiles(t *testing.T) {
	h := Handler("/ui/")
	for path, want := range map[string]int{"/ui/rcplug.css": 200, "/ui/icons.svg": 200, "/ui/": 404, "/ui/ui.go": 404, "/ui/../ui.go": 404} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/rcplug.css", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content type %q", ct)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/icons.svg", nil))
	if !strings.Contains(rec.Body.String(), `<symbol id="gear"`) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("icons: %s", rec.Header().Get("Content-Type"))
	}
}
