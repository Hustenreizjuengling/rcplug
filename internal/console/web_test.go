package console

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Every file referenced by index.html and every ES module import must exist
// in the embedded UI, so a typo cannot ship a broken console.
func TestEmbeddedUIReferencesResolve(t *testing.T) {
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := fs.Stat(web, p); return err == nil }
	html, err := fs.ReadFile(web, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?:src|href)="([^"#:]+)"`).FindAllStringSubmatch(string(html), -1) {
		if !exists(m[1]) {
			t.Errorf("index.html references missing %s", m[1])
		}
	}
	imp := regexp.MustCompile(`(?m)^\s*import\s+(?:[^'"]+\s+from\s+)?'([^']+)'`)
	n := 0
	err = fs.WalkDir(web, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		n++
		src, _ := fs.ReadFile(web, p)
		for _, m := range imp.FindAllStringSubmatch(string(src), -1) {
			target := path.Join(path.Dir(p), m[1])
			if !strings.HasPrefix(m[1], ".") || !exists(target) {
				t.Errorf("%s imports %s, which is not an embedded file", p, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("only %d modules found", n)
	}
}
