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
// The files are small (about 24 KB), cached by browsers for an hour and
// versioned with the SDK.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed rcplug.css icons.svg
var files embed.FS

// FS holds rcplug.css and icons.svg.
var FS fs.FS = files

// Handler serves the files under prefix (e.g. "/ui/").
func Handler(prefix string) http.Handler {
	fsrv := http.StripPrefix(prefix, http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if name != "rcplug.css" && name != "icons.svg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fsrv.ServeHTTP(w, r)
	})
}
