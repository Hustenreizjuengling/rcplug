// Command fakegithub serves releases from a directory the way api.github.com
// and its download host do, for tests of in-app updates:
//
//	<dir>/<owner>/<repo>/TAG          the latest release's tag, e.g. v0.9.0
//	<dir>/<owner>/<repo>/<file>       its files
//
// GET /repos/<owner>/<repo>/releases/latest lists them, GET /dl/<owner>/<repo>/<file>
// serves one. The directory is read on every request, so a test can publish
// a new release while the server runs.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18590", "listen address")
	dir := flag.String("dir", ".", "release directory")
	flag.Parse()
	base := "http://" + *addr
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		ok := func(s string) bool { return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) }
		switch {
		case len(parts) == 5 && parts[0] == "repos" && parts[3] == "releases" && parts[4] == "latest" && ok(parts[1]) && ok(parts[2]):
			repoDir := filepath.Join(*dir, parts[1], parts[2])
			tag, err := os.ReadFile(filepath.Join(repoDir, "TAG"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			entries, _ := os.ReadDir(repoDir)
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var assets []asset
			for _, e := range entries {
				if e.Name() != "TAG" && !e.IsDir() {
					assets = append(assets, asset{e.Name(), base + "/dl/" + parts[1] + "/" + parts[2] + "/" + e.Name()})
				}
			}
			t := strings.TrimSpace(string(tag))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"tag_name": t, "body": "Release " + t,
				"html_url": base + "/release/" + t, "assets": assets})
		case len(parts) == 4 && parts[0] == "dl" && ok(parts[1]) && ok(parts[2]) && ok(parts[3]):
			http.ServeFile(w, r, filepath.Join(*dir, parts[1], parts[2], parts[3]))
		default:
			http.NotFound(w, r)
		}
	})
	log.Printf("fake GitHub on %s serving %s", base, *dir)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
