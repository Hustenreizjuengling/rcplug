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

// Kept by browsers but checked again: an update's new look shows at once.
func TestHandlerRevalidates(t *testing.T) {
	h := Handler("/ui/")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/rcplug.css", nil))
	tag := rec.Header().Get("ETag")
	if tag == "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers %v", rec.Header())
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/rcplug.css", nil)
	req.Header.Set("If-None-Match", tag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("unchanged: %d, %d bytes", rec.Code, rec.Body.Len())
	}
}
