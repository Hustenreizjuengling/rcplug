// Package ui is rcplug's shared look: a stylesheet with the design tokens
// and common components (rcplug.css) and an icon sprite (icons.svg), used by
// rcplug's dashboard, its console and its plugins, so they read as one
// application. A plugin serves them next to its own pages:
//
//	mux.Handle("GET /ui/", ui.Handler("/ui/"))
//
// and links them relatively, so the page works through rcplugd's proxy and
// on its own:
//
//	<link rel="stylesheet" href="ui/rcplug.css">
//	<svg class="i"><use href="ui/icons.svg#gear"/></svg>
//
// The files are small (about 24 KB) and versioned with the SDK. Browsers
// keep them but ask each time whether they changed (ETag), so a page that
// comes with an update never runs on an older stylesheet or icon set.
package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed rcplug.css icons.svg
var files embed.FS

// FS holds rcplug.css and icons.svg.
var FS fs.FS = files

type file struct {
	data []byte
	etag string
}

var served = func() map[string]file {
	m := map[string]file{}
	for _, name := range []string{"rcplug.css", "icons.svg"} {
		b, err := files.ReadFile(name)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(b)
		m[name] = file{data: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	return m
}()

// Handler serves the files under prefix (e.g. "/ui/").
func Handler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		f, ok := served[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache") // kept, but checked again (304 while unchanged)
		w.Header().Set("ETag", f.etag)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.data))
	})
}
