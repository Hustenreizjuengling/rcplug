package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/Hustenreizjuengling/rcplug/sdk/ui"
)

// Every file the dashboard's pages reference must be embedded (or come from
// sdk/ui), so a typo cannot ship a broken dashboard.
func TestDashboardReferencesResolve(t *testing.T) {
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`(?:src|href)="/?([^"#:]+)"`)
	for _, page := range []string{"index.html", "login.html"} {
		html, err := fs.ReadFile(web, page)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range ref.FindAllStringSubmatch(string(html), -1) {
			n++
			if name, shared := strings.CutPrefix(m[1], "ui/"); shared {
				if _, err := fs.Stat(ui.FS, name); err != nil {
					t.Errorf("%s references %s, which sdk/ui does not have", page, m[1])
				}
			} else if _, err := fs.Stat(web, m[1]); err != nil {
				t.Errorf("%s references missing %s", page, m[1])
			}
		}
		if n == 0 {
			t.Errorf("%s references nothing", page)
		}
	}
}
