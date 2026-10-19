package sdk

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// Files serves a plugin's own pages from fsys (usually embedded) so a page
// load sends little: browsers keep the files but ask each time whether they
// changed (an ETag; 304 while unchanged, so an update is never missed), and
// text files go out compressed to browsers that take gzip. On a RØDECaster
// every byte to a browser goes through the same interrupt handling as the
// device's USB audio, and a page that loaded everything again at every visit
// sent a few hundred kilobytes at once.
//
// A path ending in "/" serves its index.html. The files are read once, when
// Files is called.
func Files(fsys fs.FS) http.Handler {
	files := map[string]*servedFile{}
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil
		}
		files[name] = newServedFile(name, b)
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if strings.HasSuffix(r.URL.Path, "/") || name == "" {
			name = path.Join(name, "index.html")
		}
		f, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.serve(w, r)
	})
}

// servedFile is a file with its ETag and, for text, a gzipped copy.
type servedFile struct {
	name, ctype  string
	data, gz     []byte
	etag, gzEtag string
}

func newServedFile(name string, b []byte) *servedFile {
	sum := sha256.Sum256(b)
	f := &servedFile{name: name, data: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	f.ctype = mime.TypeByExtension(path.Ext(name))
	if f.ctype == "" {
		f.ctype = http.DetectContentType(b)
	}
	if compressible(f.ctype) && len(b) >= 512 {
		var z bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
		_, _ = zw.Write(b)
		_ = zw.Close()
		if z.Len() < len(b) {
			f.gz, f.gzEtag = z.Bytes(), `"`+hex.EncodeToString(sum[:8])+`-gz"`
		}
	}
	return f
}

func (f *servedFile) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("Cache-Control", "no-cache") // kept, but checked again
	data, etag := f.data, f.etag
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if AcceptsGzip(r) {
			data, etag = f.gz, f.gzEtag
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag)
	http.ServeContent(w, r, f.name, time.Time{}, bytes.NewReader(data))
}

func compressible(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "javascript") ||
		strings.Contains(ctype, "json") || strings.Contains(ctype, "xml")
}

// AcceptsGzip reports whether the client takes gzip-compressed responses.
func AcceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); ok && strings.EqualFold(k, "q") && err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// WriteBody writes body with its content type, gzip-compressed (fast) when
// the client takes it and the body is large enough to gain from it.
func WriteBody(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Add("Vary", "Accept-Encoding")
	if len(body) < 1024 || !AcceptsGzip(r) {
		_, _ = w.Write(body)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	zw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	_, _ = zw.Write(body)
	_ = zw.Close()
}
