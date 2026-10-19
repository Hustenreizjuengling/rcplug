package host

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

func catalogPlugin(t *testing.T, name, version, extra string) []byte {
	man := `{"name":"` + name + `","version":"` + version + `","exec":["./run.sh"],"autostart":true` + extra + `}`
	return tarGz(t, map[string]string{"plugin.json": man, "run.sh": strings.Replace(loopPlugin, "%s", name+" runs", 1)},
		map[string]int64{"run.sh": 0o755})
}

func catalogEntry(st CatalogStatus, repo, name string) (CatalogEntry, bool) {
	for _, e := range st.Plugins {
		if e.Repo == repo && e.Name == name {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

func waitCatalogJob(t *testing.T, u *Updater, target string) *JobStatus {
	t.Helper()
	var j *JobStatus
	waitFor(t, "catalog job "+target, 10*time.Second, func() bool {
		j = u.Catalog().Job
		return j != nil && j.Target == target && j.State != "running"
	})
	return j
}

func TestCatalogInstallAndRemove(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "console", "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n", `{"exec":["./run.sh"]}`)
	pub, priv := testKey(t)
	otherPub, otherPriv := testKey(t)
	_, strangerPriv := testKey(t)
	fg := newFakeGitHub(t)
	fg.publish(t, "tester/rcplug-plugins", "1.0.0", priv, map[string][]byte{
		"echoer-0.2.0-aarch64.tar.gz": catalogPlugin(t, "echoer", "0.2.0",
			`,"title":"Echoer","description":"Says hello.","icon":"E","permissions":["network"],"translations":{"de":{"title":"Echo"}}`),
		"greedy-1.0.0-aarch64.tar.gz":  catalogPlugin(t, "greedy", "1.0.0", `,"permissions":["desk:write"]`),
		"console-9.9.9-aarch64.tar.gz": catalogPlugin(t, "console", "9.9.9", ""),
	}, func(m *release.Manifest) {
		for i := range m.Assets {
			if m.Assets[i].Plugin == "greedy" {
				m.Assets[i].Info.Permissions = nil // a catalog that hides what the package asks for
			}
		}
	})
	fg.publish(t, "friend/catalog", "0.3.0", otherPriv, map[string][]byte{
		"extra-0.1.0-aarch64.tar.gz": catalogPlugin(t, "extra", "0.1.0", `,"needsAudio":true`),
	}, nil)
	fg.publish(t, "stranger/catalog", "0.1.0", strangerPriv, map[string][]byte{
		"evil-0.1.0-aarch64.tar.gz": catalogPlugin(t, "evil", "0.1.0", ""),
	}, nil)

	m := NewManager(root, 19400)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	u := NewUpdater(m, "0.7.0", UpdateConfig{API: fg.srv.URL, PlatformRepo: "tester/rcplug", CatalogRepo: "tester/rcplug-plugins",
		ExtraKeys: []ed25519.PublicKey{pub}, Rate: -1})

	// Adding catalogs: owner/name and a key are required.
	for _, bad := range []CatalogSource{{GitHub: "nope"}, {GitHub: "friend/catalog"}, {GitHub: "friend/catalog", PublicKey: "short"}} {
		if err := m.AddCatalog(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := m.AddCatalog(CatalogSource{GitHub: "https://github.com/friend/catalog/", PublicKey: base64.StdEncoding.EncodeToString(otherPub)}); err != nil {
		t.Fatal(err)
	}
	// The stranger's catalog is added with the friend's key: its signature fails.
	if err := m.AddCatalog(CatalogSource{GitHub: "stranger/catalog", PublicKey: base64.StdEncoding.EncodeToString(otherPub)}); err != nil {
		t.Fatal(err)
	}

	st := u.RefreshCatalog(context.Background())
	if len(st.Sources) != 3 || !st.Sources[0].Official || st.Sources[0].Plugins != 3 || st.Sources[1].GitHub != "friend/catalog" ||
		st.Sources[1].Plugins != 1 || st.Sources[1].KeyID == "" || st.Sources[2].Error == "" {
		t.Fatalf("sources: %+v", st.Sources)
	}
	e, ok := catalogEntry(st, "tester/rcplug-plugins", "echoer")
	if !ok || e.Title != "Echoer" || e.Icon != "E" || strings.Join(e.Permissions, ",") != "network" || !e.Official ||
		e.Translations["de"]["title"] != "Echo" || e.Installed != "" || e.Size == 0 {
		t.Fatalf("echoer: %+v", e)
	}
	if e, ok := catalogEntry(st, "friend/catalog", "extra"); !ok || e.Official || strings.Join(e.Permissions, ",") != "audio" {
		t.Fatalf("extra: %+v", e)
	}
	if e, _ := catalogEntry(st, "tester/rcplug-plugins", "console"); e.Installed == "" {
		t.Errorf("console not shown as installed: %+v", e)
	}
	if _, ok := catalogEntry(st, "stranger/catalog", "evil"); ok {
		t.Fatal("a plugin from a catalog with a wrong signature is listed")
	}

	if err := u.InstallFromCatalog("tester/rcplug-plugins", "console"); err == nil {
		t.Error("installed console over the installed one")
	}
	if err := u.InstallFromCatalog("stranger/catalog", "evil"); err == nil {
		t.Error("installed from a catalog that failed its signature")
	}
	if err := u.InstallFromCatalog("tester/rcplug-plugins", "echoer"); err != nil {
		t.Fatal(err)
	}
	if j := waitCatalogJob(t, u, "install:echoer"); j.State != "done" {
		t.Fatalf("install: %s %s", j.State, j.Error)
	}
	waitFor(t, "echoer runs", 5*time.Second, func() bool {
		return strings.Contains(strings.Join(m.Logs("echoer"), "\n"), "echoer runs")
	})
	if e, _ := catalogEntry(u.Catalog(), "tester/rcplug-plugins", "echoer"); e.Installed != "0.2.0" {
		t.Errorf("after install: %+v", e)
	}
	if err := u.InstallFromCatalog("tester/rcplug-plugins", "echoer"); err == nil {
		t.Error("installed echoer twice")
	}

	if err := u.InstallFromCatalog("tester/rcplug-plugins", "greedy"); err != nil {
		t.Fatal(err)
	}
	if j := waitCatalogJob(t, u, "install:greedy"); j.State != "error" || !strings.Contains(j.Error, "did not show") {
		t.Fatalf("greedy: %s %s", j.State, j.Error)
	}
	for _, p := range []string{"plugins/greedy", ".update"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", p)
		}
	}

	// Removing: console stays; echoer goes, with its data on request.
	if err := m.RemovePlugin("console", true); err == nil {
		t.Error("removed console")
	}
	os.MkdirAll(filepath.Join(root, "data", "echoer"), 0o755)
	os.WriteFile(filepath.Join(root, "config", "echoer.json"), []byte(`{}`), 0o644)
	srv := NewServer(m, nil, "test")
	srv.Updates = u
	h := srv.Handler()
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://127.0.0.1"+path, bytes.NewBufferString(body))
		req.Header.Set("X-Rcplug", "1")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/api/plugins/echoer/remove", `{"purge":true}`); rec.Code != 400 {
		t.Fatalf("remove without confirm: %d %s", rec.Code, rec.Body)
	}
	if rec := post("/api/plugins/echoer/remove", `{"confirm":"echoer","purge":true}`); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if m.get("echoer") != nil {
		t.Error("echoer still loaded")
	}
	for _, p := range []string{"plugins/echoer", "data/echoer", "config/echoer.json"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", p)
		}
	}
	if _, ok := m.loadState().Enabled["echoer"]; ok {
		t.Error("echoer's state kept")
	}
	if rec := post("/api/plugins/echoer/remove", `{"confirm":"echoer"}`); rec.Code != 404 {
		t.Errorf("remove twice: %d", rec.Code)
	}

	// The catalog over HTTP; removing a catalog.
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/catalog", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var doc CatalogStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Plugins) != 4 || !doc.Enabled {
		t.Fatalf("GET api/catalog: %v %s", err, rec.Body)
	}
	if rec := post("/api/catalog/sources", `{"github":"stranger/catalog","remove":true}`); rec.Code != 200 {
		t.Fatalf("remove a catalog: %d %s", rec.Code, rec.Body)
	}
	if got := m.Catalogs(); len(got) != 1 || got[0].GitHub != "friend/catalog" {
		t.Errorf("catalogs: %+v", got)
	}
	if rec := post("/api/catalog/install", `{"repo":"tester/rcplug-plugins","plugin":"echoer"}`); rec.Code != 200 {
		t.Fatalf("install over HTTP: %d %s", rec.Code, rec.Body)
	}
	if j := waitCatalogJob(t, u, "install:echoer"); j.State != "done" {
		t.Fatalf("second install: %s %s", j.State, j.Error)
	}
}

// Leftovers of an interrupted update or removal go at the next start, but
// not while an installer may still use them.
func TestCleanLeftovers(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, 9690)
	old := time.Now().Add(-time.Hour)
	for _, d := range []string{".update/payload", ".removed-sonar-1", ".removed-fresh-2"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{".update", ".removed-sonar-1"} {
		if err := os.Chtimes(filepath.Join(root, d), old, old); err != nil {
			t.Fatal(err)
		}
	}
	gone := m.CleanLeftovers()
	if len(gone) != 2 {
		t.Fatalf("removed %v", gone)
	}
	if _, err := os.Stat(filepath.Join(root, ".removed-fresh-2")); err != nil {
		t.Fatal("a fresh leftover was removed")
	}
}
