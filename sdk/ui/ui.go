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
// comes with an update never runs on an older stylesheet or icon set; they go
// out compressed (sdk.Files).
package ui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

//go:embed rcplug.css icons.svg
var files embed.FS

// FS holds rcplug.css and icons.svg.
var FS fs.FS = files

var handler = sdk.Files(files)

// Handler serves the files under prefix (e.g. "/ui/").
func Handler(prefix string) http.Handler {
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "rcplug.css" && r.URL.Path != "icons.svg" {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}
