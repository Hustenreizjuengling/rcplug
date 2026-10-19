package host

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

// fakeGitHub serves /repos/<owner>/<repo>/releases/latest and the release
// files, like api.github.com and its download host.
type fakeGitHub struct {
	srv      *httptest.Server
	mu       sync.Mutex
	releases map[string]fakeRelease // "owner/repo" → latest release
}

type fakeRelease struct {
	tag   string
	files map[string][]byte
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	fg := &fakeGitHub{releases: map[string]fakeRelease{}}
	fg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		defer fg.mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 5 && parts[0] == "repos" && parts[3] == "releases" && parts[4] == "latest":
			rel, ok := fg.releases[parts[1]+"/"+parts[2]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var assets []asset
			for name := range rel.files {
				assets = append(assets, asset{name, fg.srv.URL + "/dl/" + parts[1] + "/" + parts[2] + "/" + name})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"tag_name": rel.tag, "body": "notes for " + rel.tag,
				"html_url": "https://example.invalid/" + rel.tag, "assets": assets})
		case len(parts) == 4 && parts[0] == "dl":
			rel := fg.releases[parts[1]+"/"+parts[2]]
			b, ok := rel.files[parts[3]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fg.srv.Close)
	return fg
}

// publish signs files as the latest release of repo.
func (fg *fakeGitHub) publish(t *testing.T, repo, version string, key ed25519.PrivateKey, files map[string][]byte, tweak func(*release.Manifest)) {
	t.Helper()
	dir := t.TempDir()
	m := release.Manifest{Format: 1, Project: repo[strings.IndexByte(repo, '/')+1:], Version: version}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		a, err := release.Describe(p)
		if err != nil {
			t.Fatal(err)
		}
		m.Assets = append(m.Assets, a)
	}
	if tweak != nil {
		tweak(&m)
	}
	data, _ := json.Marshal(m)
	all := map[string][]byte{release.ManifestName: data, release.SignatureName: release.Sign(data, key)}
	for k, v := range files {
		all[k] = v
	}
	fg.mu.Lock()
	fg.releases[repo] = fakeRelease{tag: "v" + version, files: all}
	fg.mu.Unlock()
}

func tarGz(t *testing.T, files map[string]string, modes map[string]int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		mode := int64(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const loopPlugin = "#!/bin/sh\necho \"%s\"\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n"

func pluginManifest(version string) string {
	return `{"name":"echoer","version":"` + version + `","exec":["./run.sh"],"autostart":true,"update":{"github":"tester/rcplug-plugins"}}`
}

func newTestUpdater(t *testing.T, root string, fg *fakeGitHub, pub ed25519.PublicKey) (*Manager, *Updater) {
	m := NewManager(root, 19300)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	u := NewUpdater(m, "0.5.0", UpdateConfig{API: fg.srv.URL, PlatformRepo: "tester/rcplug", ExtraKeys: []ed25519.PublicKey{pub}})
	return m, u
}

func target(st UpdateStatus, name string) UpdateTarget {
	for _, tg := range st.Targets {
		if tg.Target == name {
			return tg
		}
	}
	return UpdateTarget{}
}

func waitJob(t *testing.T, u *Updater, want string) *JobStatus {
	t.Helper()
	var j *JobStatus
	waitFor(t, "update job "+want, 10*time.Second, func() bool {
		j = u.Status().Job
		return j != nil && j.State != "running"
	})
	if j.State != want {
		t.Fatalf("job ended %s: %s\n%s", j.State, j.Error, strings.Join(j.Log, "\n"))
	}
	return j
}

func TestUpdatePlugin(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "echoer", strings.Replace(loopPlugin, "%s", "old", 1), pluginManifest("0.1.0"))
	os.MkdirAll(filepath.Join(root, "data", "echoer"), 0o755)
	os.WriteFile(filepath.Join(root, "data", "echoer", "keep.json"), []byte(`{"kept":true}`), 0o644)
	pub, priv := testKey(t)
	fg := newFakeGitHub(t)
	pkg := tarGz(t, map[string]string{"plugin.json": pluginManifest("0.2.0"), "run.sh": strings.Replace(loopPlugin, "%s", "new code", 1)},
		map[string]int64{"run.sh": 0o755})
	fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, map[string][]byte{"echoer-0.2.0-aarch64.tar.gz": pkg}, nil)

	m, u := newTestUpdater(t, root, fg, pub)
	waitFor(t, "plugin running", 5*time.Second, func() bool { s, _ := statusOf(m, "echoer"); return s.State == StateRunning })

	st := u.Check(context.Background())
	tg := target(st, "plugin:echoer")
	if !tg.Available || tg.Latest != "0.2.0" || tg.Current != "0.1.0" || tg.Error != "" {
		t.Fatalf("plugin target = %+v", tg)
	}
	if pt := target(st, "rcplug"); pt.Available || !strings.Contains(pt.Error, "no release yet") {
		t.Fatalf("platform target = %+v", pt)
	}
	if err := u.Install("plugin:echoer"); err != nil {
		t.Fatal(err)
	}
	waitJob(t, u, "done")
	if j := u.Status().Job; j == nil || j.Phase != "install" {
		t.Fatalf("job after the update: %+v", j)
	}

	man, err := LoadManifest(filepath.Join(root, "plugins", "echoer"))
	if err != nil || man.Version != "0.2.0" {
		t.Fatalf("installed manifest = %+v, %v", man, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "data", "echoer", "keep.json")); string(b) != `{"kept":true}` {
		t.Fatal("the plugin's data was not kept")
	}
	if fi, err := os.Stat(filepath.Join(root, "plugins", "echoer", "run.sh")); err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("run.sh lost its execute bit: %v", err)
	}
	waitFor(t, "new code running", 5*time.Second, func() bool {
		return strings.Contains(strings.Join(m.Logs("echoer"), "\n"), "new code")
	})
	if _, err := os.Stat(filepath.Join(root, ".update")); !os.IsNotExist(err) {
		t.Fatal(".update left behind")
	}
	if tg := target(u.Status(), "plugin:echoer"); tg.Available || tg.Current != "0.2.0" {
		t.Fatalf("after the update: %+v", tg)
	}
	if err := u.Install("plugin:echoer"); err == nil {
		t.Fatal("installed the same update twice")
	}
}

func TestUpdatePlatform(t *testing.T) {
	root := t.TempDir()
	pub, priv := testKey(t)
	fg := newFakeGitHub(t)
	pkg := tarGz(t, map[string]string{"install.sh": "#!/bin/sh\necho installing\n", "VERSION": "0.6.0\n", "rcplugd": "binary"},
		map[string]int64{"install.sh": 0o755, "rcplugd": 0o755})
	fg.publish(t, "tester/rcplug", "0.6.0", priv, map[string][]byte{"rcplug-0.6.0-aarch64.tar.gz": pkg}, nil)
	_, u := newTestUpdater(t, root, fg, pub)
	var spawned string
	u.spawn = func(payload, logFile, resultFile string) error { spawned = payload; return nil }

	if pt := target(u.Check(context.Background()), "rcplug"); !pt.Available || pt.Latest != "0.6.0" || pt.Notes != "notes for v0.6.0" {
		t.Fatalf("platform target = %+v", pt)
	}
	if err := u.Install("rcplug"); err != nil {
		t.Fatal(err)
	}
	waitJob(t, u, "restarting")
	if b, err := os.ReadFile(filepath.Join(spawned, "install.sh")); err != nil || !strings.Contains(string(b), "installing") {
		t.Fatalf("installer not given the unpacked package: %q, %v", spawned, err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(root, "logs", "update.log"))), "updating rcplug 0.5.0 to 0.6.0") {
		t.Fatal("update.log has no entry")
	}

	// What the installer records shows up as the last update.
	os.WriteFile(filepath.Join(root, "logs", "update.result"), []byte("1 2026-10-18T03:00:00Z\n"), 0o644)
	f, _ := os.OpenFile(filepath.Join(root, "logs", "update.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("rcplug: the new version did not start; going back to the previous one\n")
	f.Close()
	lu := u.Status().Last
	if lu == nil || lu.OK || lu.At != "2026-10-18T03:00:00Z" || lu.From != "0.5.0" || lu.To != "0.6.0" ||
		!strings.Contains(strings.Join(lu.Log, "\n"), "going back") {
		t.Fatalf("last update = %+v", lu)
	}
	// This process lives on after the installer failed: the job ends, and
	// the next update is not refused as busy.
	if j := u.Status().Job; j == nil || j.State != "error" {
		t.Fatalf("job after a failed installer: %+v", j)
	}
	if err := u.Install("rcplug"); err == ErrUpdateBusy {
		t.Fatal("still busy after the installer failed")
	}
}

func mustRead(t *testing.T, f string) []byte {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The real installer runs on in its own session and records the outcome.
func TestSpawnInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no device installer on Windows")
	}
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	os.MkdirAll(payload, 0o755)
	marker := filepath.Join(dir, "ran")
	os.WriteFile(filepath.Join(payload, "install.sh"), []byte("#!/bin/sh\necho hello >\""+marker+"\"\necho done\n"), 0o755)
	logFile, result := filepath.Join(dir, "update.log"), filepath.Join(dir, "update.result")
	if err := spawnInstaller(payload, logFile, result); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "installer result", 5*time.Second, func() bool { b, _ := os.ReadFile(result); return strings.HasPrefix(string(b), "0 ") })
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("install.sh did not run")
	}
	if _, err := os.Stat(payload); !os.IsNotExist(err) {
		t.Fatal("the unpacked package was not removed")
	}
	if !strings.Contains(string(mustRead(t, logFile)), "done") {
		t.Fatal("installer output not logged")
	}
}

func TestUpdateRefusesBadReleases(t *testing.T) {
	pub, priv := testKey(t)
	_, otherPriv := testKey(t)
	pkg := tarGz(t, map[string]string{"plugin.json": pluginManifest("0.2.0"), "run.sh": "#!/bin/sh\n"}, nil)
	files := map[string][]byte{"echoer-0.2.0-aarch64.tar.gz": pkg}
	cases := []struct {
		name    string
		publish func(fg *fakeGitHub)
		check   string // error expected from the check
		install string // error expected from the install job
	}{
		{"untrusted key", func(fg *fakeGitHub) { fg.publish(t, "tester/rcplug-plugins", "0.2.0", otherPriv, files, nil) }, "trusted key", ""},
		{"wrong project", func(fg *fakeGitHub) {
			fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, files, func(m *release.Manifest) { m.Project = "rcplug" })
		}, "manifest is for", ""},
		{"tag differs", func(fg *fakeGitHub) {
			fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, files, func(m *release.Manifest) { m.Version = "0.3.0" })
		}, "manifest says version", ""},
		{"unsigned", func(fg *fakeGitHub) {
			fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, files, nil)
			delete(fg.releases["tester/rcplug-plugins"].files, release.SignatureName)
		}, "not signed", ""},
		{"swapped file", func(fg *fakeGitHub) {
			fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, files, nil)
			fg.releases["tester/rcplug-plugins"].files["echoer-0.2.0-aarch64.tar.gz"] = tarGz(t, map[string]string{"plugin.json": pluginManifest("0.2.0"), "run.sh": "#!/bin/sh\nevil\n"}, nil)
		}, "", "does not match the signed manifest"},
		{"package for another plugin", func(fg *fakeGitHub) {
			other := tarGz(t, map[string]string{"plugin.json": strings.Replace(pluginManifest("0.2.0"), `"echoer"`, `"other"`, 1), "run.sh": "#!/bin/sh\n"}, nil)
			fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, map[string][]byte{"echoer-0.2.0-aarch64.tar.gz": other},
				func(m *release.Manifest) { m.Assets[0].Plugin = "echoer" })
		}, "", `is for the plugin "other"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writePlugin(t, root, "echoer", strings.Replace(loopPlugin, "%s", "old", 1), pluginManifest("0.1.0"))
			fg := newFakeGitHub(t)
			c.publish(fg)
			_, u := newTestUpdater(t, root, fg, pub)
			tg := target(u.Check(context.Background()), "plugin:echoer")
			if c.check != "" {
				if tg.Available || !strings.Contains(tg.Error, c.check) {
					t.Fatalf("target = %+v, want error %q", tg, c.check)
				}
				if err := u.Install("plugin:echoer"); err == nil {
					t.Fatal("install started for a refused release")
				}
				return
			}
			if err := u.Install("plugin:echoer"); err != nil {
				t.Fatal(err)
			}
			j := waitJob(t, u, "error")
			if !strings.Contains(j.Error, c.install) {
				t.Fatalf("job error = %q, want %q", j.Error, c.install)
			}
			if man, _ := LoadManifest(filepath.Join(root, "plugins", "echoer")); man == nil || man.Version != "0.1.0" {
				t.Fatal("the old plugin was replaced")
			}
			if _, err := os.Stat(filepath.Join(root, ".update")); !os.IsNotExist(err) {
				t.Fatal(".update left behind after a failed install")
			}
		})
	}
}

func TestNoUpdateWhenNotNewer(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "echoer", strings.Replace(loopPlugin, "%s", "old", 1), pluginManifest("0.2.0"))
	pub, priv := testKey(t)
	fg := newFakeGitHub(t)
	pkg := tarGz(t, map[string]string{"plugin.json": pluginManifest("0.2.0"), "run.sh": "#!/bin/sh\n"}, nil)
	fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, map[string][]byte{"echoer-0.2.0-aarch64.tar.gz": pkg}, nil)
	_, u := newTestUpdater(t, root, fg, pub)
	if tg := target(u.Check(context.Background()), "plugin:echoer"); tg.Available || tg.Latest != "0.2.0" {
		t.Fatalf("target = %+v", tg)
	}
	if err := u.Install("plugin:echoer"); err == nil {
		t.Fatal("installed a release that is not newer")
	}
}

func TestAllowedURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.github.com/x": true, "http://127.0.0.1:8000/x": true, "http://localhost/x": true, "http://[::1]:9/x": true,
		"http://api.github.com/x": false, "http://192.168.1.2/x": false, "file:///etc/passwd": false, "ftp://x/y": false,
	} {
		u, _ := url.Parse(raw)
		if err := allowedURL(u); (err == nil) != ok {
			t.Errorf("allowedURL(%s) = %v", raw, err)
		}
	}
}

func TestExtractRejectsEscapes(t *testing.T) {
	for name, hdr := range map[string]tar.Header{
		"parent":   {Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"absolute": {Name: "/etc/evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink":  {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&hdr)
		tw.Close()
		gz.Close()
		file := filepath.Join(t.TempDir(), "p.tar.gz")
		os.WriteFile(file, buf.Bytes(), 0o644)
		if err := extractTarGz(file, filepath.Join(t.TempDir(), "out"), 1<<20, 0, nil); err == nil {
			t.Errorf("%s: extracted", name)
		}
	}
}

func TestUpdateAPI(t *testing.T) {
	root := t.TempDir()
	pub, _ := testKey(t)
	fg := newFakeGitHub(t)
	m, u := newTestUpdater(t, root, fg, pub)
	srv := NewServer(m, nil, "0.5.0")
	srv.Updates = u
	h := srv.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/updates", nil))
	var st UpdateStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Enabled || st.Current != "0.5.0" || len(st.KeyIDs) < 2 {
		t.Fatalf("GET /api/updates = %s", rec.Body.String())
	}
	// State changes need the CSRF header, like every other POST.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "http://127.0.0.1/api/updates/check", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("check without X-Rcplug accepted")
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/updates/install?target=rcplug", nil)
	req.Header.Set("X-Rcplug", "1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("install without a check = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateProgress(t *testing.T) {
	j := &updateJob{}
	j.progress("download", 0, 1000)
	if st := j.snapshot(); st.Phase != "download" || st.Total != 1000 || st.ETA != 0 {
		t.Fatalf("start: %+v", st)
	}
	j.mu.Lock()
	j.phaseStart = time.Now().Add(-2 * time.Second)
	j.mu.Unlock()
	j.progress("download", 500, 1000) // half in 2 s: about 2 s left
	if st := j.snapshot(); st.Done != 500 || st.ETA != 2 {
		t.Fatalf("half way: %+v", st)
	}
	j.progress("unpack", 10, 1000) // a new phase starts its own clock
	if st := j.snapshot(); st.Phase != "unpack" || st.ETA != 0 {
		t.Fatalf("unpack: %+v", st)
	}
}

func TestDailyCheckIsOptIn(t *testing.T) {
	oldTick, oldFirst, oldEvery := autoTick, autoFirst, autoEvery
	autoTick, autoFirst, autoEvery = 20*time.Millisecond, 20*time.Millisecond, time.Hour
	t.Cleanup(func() { autoTick, autoFirst, autoEvery = oldTick, oldFirst, oldEvery })
	root := t.TempDir()
	writePlugin(t, root, "echoer", strings.Replace(loopPlugin, "%s", "old", 1), pluginManifest("0.1.0"))
	pub, priv := testKey(t)
	fg := newFakeGitHub(t)
	pkg := tarGz(t, map[string]string{"plugin.json": pluginManifest("0.2.0"), "run.sh": loopPlugin}, map[string]int64{"run.sh": 0o755})
	fg.publish(t, "tester/rcplug-plugins", "0.2.0", priv, map[string][]byte{"echoer-0.2.0-aarch64.tar.gz": pkg}, nil)
	m, u := newTestUpdater(t, root, fg, pub)
	srv := NewServer(m, nil, "0.5.0")
	srv.Updates = u
	h := srv.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { u.RunAutoCheck(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	time.Sleep(150 * time.Millisecond)
	if st := u.Status(); st.CheckedAt != nil || st.AutoCheck {
		t.Fatalf("checked without being switched on: %+v", st)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/updates/auto?on=true", nil)
	req.Header.Set("X-Rcplug", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !m.AutoCheck() {
		t.Fatalf("switch on: %d %s", rec.Code, rec.Body.String())
	}
	waitFor(t, "the daily check", 5*time.Second, func() bool { return u.Status().CheckedAt != nil })
	if st := u.Status(); !st.AutoCheck || st.Available != 1 {
		t.Fatalf("after the check: autoCheck %v, available %d", st.AutoCheck, st.Available)
	}
	if u.Status().Job != nil {
		t.Fatal("the daily check installed something")
	}
	// The choice survives a restart of the host.
	if !NewManager(root, 19400).AutoCheck() {
		t.Fatal("the choice was not kept")
	}
}
