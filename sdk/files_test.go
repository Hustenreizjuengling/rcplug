package sdk

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFiles(t *testing.T) {
	js := strings.Repeat("export const answer = 42;\n", 200)
	h := Files(fstest.MapFS{
		"index.html":     {Data: []byte("<!DOCTYPE html><title>t</title>" + strings.Repeat(" ", 600))},
		"js/main.js":     {Data: []byte(js)},
		"icon.png":       {Data: []byte("\x89PNG\r\n\x1a\n....")},
		"sub/index.html": {Data: []byte("<p>sub</p>")},
	})
	get := func(path, enc, etag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Compressed for a browser that takes gzip, kept (ETag) and checked again.
	w := get("/js/main.js", "gzip, deflate, br", "")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Cache-Control") != "no-cache" ||
		!strings.Contains(w.Header().Get("Content-Type"), "javascript") || !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != js {
		t.Fatal("the compressed file is not the file")
	}
	if w.Body.Len() > len(js)/5 {
		t.Errorf("%d bytes compressed from %d", w.Body.Len(), len(js))
	}
	etag := w.Header().Get("ETag")
	if w := get("/js/main.js", "gzip", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("unchanged file sent again: %d", w.Code)
	}

	// Plain for a client without gzip (or refusing it), with its own ETag.
	for _, enc := range []string{"", "identity", "gzip;q=0"} {
		w := get("/js/main.js", enc, "")
		if w.Header().Get("Content-Encoding") != "" || w.Body.String() != js || w.Header().Get("ETag") == etag {
			t.Fatalf("Accept-Encoding %q: %v", enc, w.Header())
		}
	}
	if AcceptsGzip(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("no header means no gzip")
	}

	// Index pages, binary files as they are, nothing else.
	if w := get("/", "gzip", ""); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/: %d %v", w.Code, w.Header())
	}
	if w := get("/sub/", "", ""); w.Body.String() != "<p>sub</p>" {
		t.Fatalf("/sub/: %q", w.Body.String())
	}
	if w := get("/icon.png", "gzip", ""); w.Header().Get("Content-Encoding") != "" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("png: %v", w.Header())
	}
	for _, p := range []string{"/missing.js", "/js/"} {
		if w := get(p, "", ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestWriteBody(t *testing.T) {
	body := bytes.Repeat([]byte(`{"a":1},`), 1000)
	r := httptest.NewRequest("GET", "/api/tree", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	WriteBody(w, r, "application/json", body)
	zr, err := gzip.NewReader(w.Body)
	if err != nil || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("%v %v", err, w.Header())
	}
	if b, _ := io.ReadAll(zr); !bytes.Equal(b, body) {
		t.Fatal("not the body")
	}
	w = httptest.NewRecorder()
	WriteBody(w, httptest.NewRequest("GET", "/", nil), "application/json", body)
	if w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatal("compressed for a client without gzip")
	}
	w = httptest.NewRecorder()
	WriteBody(w, r, "application/json", []byte(`{}`))
	if w.Header().Get("Content-Encoding") != "" || w.Body.String() != "{}" {
		t.Fatal("a small body compressed")
	}
}
