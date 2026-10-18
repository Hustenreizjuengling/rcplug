package host

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/Hustenreizjuengling/rcplug/sdk"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReloadRemovesVanishedPluginsAndSorts(t *testing.T) {
	root := t.TempDir()
	idle := "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n"
	writePlugin(t, root, "zeta", idle, `{"exec":["./run.sh"],"autostart":true,"order":1}`)
	writePlugin(t, root, "alpha", idle, `{"exec":["./run.sh"],"autostart":true,"order":5}`)
	m := NewManager(root, 9300)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	list := m.List()
	if len(list) != 2 || list[0].Name != "zeta" || list[1].Name != "alpha" {
		t.Fatalf("order: %+v", list)
	}
	waitFor(t, "zeta running", 3*time.Second, func() bool { s, _ := statusOf(m, "zeta"); return s.Running })
	if err := os.RemoveAll(filepath.Join(root, "plugins", "zeta")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := statusOf(m, "zeta"); ok {
		t.Fatal("removed plugin still listed")
	}
	// Restart of a disabled plugin must not silently enable it.
	m.Disable("alpha")
	m.Restart("alpha")
	time.Sleep(200 * time.Millisecond)
	if s, _ := statusOf(m, "alpha"); s.Enabled || s.Running {
		t.Fatalf("restart enabled a disabled plugin: %+v", s)
	}
}

func TestSameOriginAndProxyHeaders(t *testing.T) {
	// Backend plugin that echoes what it received.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.EscapedPath()+"|"+r.Header.Get("X-Forwarded-Prefix")+"|"+r.Header.Get("X-Forwarded-Host")+"|"+r.Header.Get("X-Forwarded-For"))
	})}
	go backend.Serve(ln)
	defer backend.Close()

	root := t.TempDir()
	writePlugin(t, root, "echo", "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n",
		`{"exec":["./run.sh"],"autostart":false,"httpProxy":"127.0.0.1:`+strconv.Itoa(port)+`"}`)
	m := NewManager(root, 9400)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	srv := httptest.NewServer(NewServer(m, nil, "test").Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/plugins/echo/api/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	parts := strings.Split(string(body), "|")
	if len(parts) != 4 || parts[0] != "/api/x" || parts[1] != "/plugins/echo" || parts[2] != host || parts[3] != "127.0.0.1" {
		t.Fatalf("proxied request: %q", body)
	}
	// Neither the dashboard nor plugin pages may be framed by other sites.
	if got := resp.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Errorf("proxied response CSP = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("proxied response X-Frame-Options = %q", got)
	}

	post := func(origin string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/reload", nil)
		req.Header.Set("X-Rcplug", "1")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := post("http://evil.example"); c != http.StatusForbidden {
		t.Errorf("foreign origin: %d", c)
	}
	if c := post(srv.URL); c != http.StatusOK {
		t.Errorf("same origin: %d", c)
	}
	if c := post(""); c != http.StatusOK {
		t.Errorf("no origin: %d", c)
	}
	// The host's own API needs the CSRF header.
	r, _ := http.Post(srv.URL+"/api/reload", "application/json", nil)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("reload without X-Rcplug: %d", r.StatusCode)
	}

	// An encoded slash reaches the plugin as it was sent.
	if b := get(t, srv.URL+"/plugins/echo/a%2Fb"); !strings.HasPrefix(b, "/a%2Fb|") {
		t.Errorf("escaped path: %q", b)
	}
	// The redirect to the trailing slash keeps the query.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := noRedirect.Get(srv.URL + "/plugins/echo?strip=physical1"); err != nil || r.Header.Get("Location") != "/plugins/echo/?strip=physical1" {
		t.Errorf("redirect: %v %v", r.Header.Get("Location"), err)
	}
	// A WebSocket handshake from a foreign page is refused, from the same origin passed on.
	ws := func(origin string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/plugins/echo/ws", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Origin", origin)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := ws("http://evil.example"); c != http.StatusForbidden {
		t.Errorf("cross-site WebSocket: %d", c)
	}
	if c := ws(srv.URL); c == http.StatusForbidden {
		t.Errorf("same-origin WebSocket refused")
	}
}

func get(t *testing.T, u string) string {
	t.Helper()
	r, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// A plugin that does not answer gets a page that retries, for browsers.
func TestUnreachablePlugin(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there
	root := t.TempDir()
	writePlugin(t, root, "down", "#!/bin/sh\nexit 0\n", `{"exec":["./run.sh"],"httpProxy":"127.0.0.1:`+strconv.Itoa(port)+`"}`)
	m := NewManager(root, 9450)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	srv := httptest.NewServer(NewServer(m, nil, "test").Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/plugins/down/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusBadGateway || !strings.Contains(string(b), `http-equiv="refresh"`) || !strings.Contains(string(b), "<b style=\"color:#e7edf3\">down</b>") {
		t.Errorf("page for browsers: %d %s", r.StatusCode, b)
	}
	if b := get(t, srv.URL+"/plugins/down/api/x"); !strings.Contains(b, "not reachable") || strings.Contains(b, "<") {
		t.Errorf("answer for scripts: %q", b)
	}
}

func TestHostAllowlistStopsDNSRebinding(t *testing.T) {
	s := NewServer(NewManager(t.TempDir(), 9500), nil, "test")
	s.AllowHosts = []string{"studio.example.org", ".myhome.net"}
	hostname, _ := os.Hostname()
	for host, want := range map[string]bool{
		"192.168.1.50:8080": true, "[fe80::1]:8080": true, "localhost:8080": true,
		"rodecaster.local": true, "rodecaster.fritz.box:8080": true, hostname + ":8080": true,
		"studio.example.org": true, "duo.myhome.net": true,
		"evil.example.com": false, "rebind.attacker.net:8080": false, "myhome.net.evil.com": false,
	} {
		if got := s.hostAllowed(host); got != want {
			t.Errorf("%s: %v, want %v", host, got, want)
		}
	}
	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host: %d", rec.Code)
	}
}

func TestUserConfigEnvAndMasking(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "env", "#!/bin/sh\necho \"dir=$RCPLUG_DATA_DIR host=$RCPLUG_HOST_URL x=$EXTRA args=$*\"\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n",
		`{"exec":["./run.sh","--pin","1234"],"autostart":true}`)
	if err := os.MkdirAll(filepath.Join(root, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "env.json"), []byte(`{"args":["--read-only"],"env":{"EXTRA":"yes"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(root, 9600)
	m.HostURL = "http://127.0.0.1:18080"
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	waitFor(t, "output", 3*time.Second, func() bool {
		for _, l := range m.Logs("env") {
			if strings.Contains(l, "x=yes") {
				return true
			}
		}
		return false
	})
	logs := strings.Join(m.Logs("env"), "\n")
	for _, l := range m.Logs("env") {
		if strings.Contains(l, "started pid=") && (strings.Contains(l, "1234") || !strings.Contains(l, "--pin ***")) {
			t.Errorf("secret not masked in the host's start line: %s", l)
		}
	}
	if !strings.Contains(logs, "dir="+filepath.Join(root, "data", "env")) || !strings.Contains(logs, "host=http://127.0.0.1:18080") ||
		!strings.Contains(logs, "--read-only") {
		t.Errorf("environment/args not passed:\n%s", logs)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "env")); err != nil {
		t.Errorf("data dir not created: %v", err)
	}
}

func TestPortAssignedWhenUIAppearsAndReloadIsRaceFree(t *testing.T) {
	root := t.TempDir()
	idle := "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n"
	writePlugin(t, root, "late", idle, `{"exec":["./run.sh"],"autostart":true}`)
	m := NewManager(root, 9700)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	if s, _ := statusOf(m, "late"); s.Port != 0 {
		t.Fatalf("port without UI: %d", s.Port)
	}
	writePlugin(t, root, "late", idle, `{"exec":["./run.sh"],"autostart":true,"httpProxy":"127.0.0.1:{{port}}","title":"Late"}`)
	srv := NewServer(m, nil, "test")
	done := make(chan struct{})
	go func() { // concurrent readers while reloading (run with -race)
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = m.List()
			if p := m.get("late"); p != nil {
				_, _ = srv.proxyFor(p)
			}
		}
	}()
	for i := 0; i < 5; i++ {
		if _, err := m.Load(); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if s, _ := statusOf(m, "late"); s.Port != 9700 || s.Title != "Late" {
		t.Fatalf("after reload: %+v", s)
	}
}

func TestStopStartDoNotOverlap(t *testing.T) {
	root := t.TempDir()
	// The process takes a moment to exit after SIGINT and records overlaps.
	writePlugin(t, root, "slow", "#!/bin/sh\nif [ -e \"$RCPLUG_DATA_DIR/running\" ]; then echo OVERLAP; fi\ntouch \"$RCPLUG_DATA_DIR/running\"\n"+
		"trap 'sleep 0.3; rm -f \"$RCPLUG_DATA_DIR/running\"; exit 0' INT TERM\nwhile true; do sleep 0.1; done\n",
		`{"exec":["./run.sh"],"autostart":true}`)
	m := NewManager(root, 9800)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	waitFor(t, "running", 3*time.Second, func() bool { s, _ := statusOf(m, "slow"); return s.Running })
	go m.Disable("slow")
	time.Sleep(20 * time.Millisecond)
	m.Enable("slow")
	waitFor(t, "running again", 5*time.Second, func() bool { s, _ := statusOf(m, "slow"); return s.Running && s.Enabled })
	time.Sleep(500 * time.Millisecond)
	if s, _ := statusOf(m, "slow"); !s.Running || !s.Enabled {
		t.Fatalf("final state %+v", s)
	}
	if strings.Contains(strings.Join(m.Logs("slow"), "\n"), "OVERLAP") {
		t.Fatal("two plugin processes overlapped")
	}
}

func TestMaskArgs(t *testing.T) {
	got := strings.Join(maskArgs([]string{"./x", "--pin", "1", "--token=abc", "--addr", "a", "-password", "p",
		"--hook", "https://u:pw@hooks.example/abc?t=1", "--api=http://127.0.0.1:8080", "--x=https://h.example/p"}), " ")
	want := "./x --pin *** --token=*** --addr a -password *** --hook https://hooks.example/*** --api=http://127.0.0.1:8080 --x=https://h.example/***"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestManifestProxyMustBeLoopback(t *testing.T) {
	for target, ok := range map[string]bool{
		"127.0.0.1:{{port}}":    true,
		"http://127.0.0.1:8081": true,
		"localhost:9000":        true,
		"[::1]:{{port}}":        true,
		"192.168.1.10:80":       false,
		"example.com:80":        false,
		"127.0.0.1":             false,
		"127.0.0.1:80/evil?x=1": false,
		"https://127.0.0.1:443": false,
	} {
		dir := filepath.Join(t.TempDir(), "p")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := `{"exec":["./x"],"httpProxy":"` + target + `"}`
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadManifest(dir)
		if (err == nil) != ok {
			t.Errorf("httpProxy %q: err=%v, want ok=%v", target, err, ok)
		}
	}
}

func TestManifestTranslations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"exec":["./x"],"title":"Hello","translations":{"de":{"title":"Hallo"}},"bogus":1}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Translations["de"].Title != "Hallo" {
		t.Errorf("translations = %+v", m.Translations)
	}
	if len(m.Unknown) != 1 || m.Unknown[0] != "bogus" {
		t.Errorf("unknown = %v, want [bogus]", m.Unknown)
	}
}

// The manifests shipped in plugins/ must load cleanly on this host.
func TestShippedManifests(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no plugins found: %v", err)
	}
	ver, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		m, err := LoadManifest(dir)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		if len(m.Unknown) > 0 {
			t.Errorf("%s: unknown fields %v", dir, m.Unknown)
		}
		if m.Description == "" {
			t.Errorf("%s: shipped plugins need a description", dir)
		}
		// Built-in plugins are released together with the host.
		if want := strings.TrimSpace(string(ver)); m.Version != want {
			t.Errorf("%s: version %q, want %q from VERSION", dir, m.Version, want)
		}
	}
}

func TestManifestRejectsBadDirectoryNames(t *testing.T) {
	for name, ok := range map[string]bool{"sonar": true, "my-plugin_2": true, "Upper": false, "-x": false, "a b": false, "ä": false} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"exec":["./x"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(dir); (err == nil) != ok {
			t.Errorf("%q: err=%v, want ok=%v", name, err, ok)
		}
	}
}

func TestManifestActions(t *testing.T) {
	load := func(actions string, proxy bool) error {
		dir := filepath.Join(t.TempDir(), "p")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		hp := ""
		if proxy {
			hp = `"httpProxy":"127.0.0.1:{{port}}",`
		}
		raw := `{"exec":["./x"],` + hp + `"actions":` + actions + `}`
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadManifest(dir)
		return err
	}
	ok := `[{"id":"open","context":"channel","title":"Sonar","path":"?strip={strip}"}]`
	if err := load(ok, true); err != nil {
		t.Errorf("valid action refused: %v", err)
	}
	if err := load(`[{"id":"x","context":"some-future-context","title":"X","path":""}]`, true); err != nil {
		t.Errorf("unknown contexts must load (newer plugins on older consoles): %v", err)
	}
	for name, bad := range map[string]string{
		"no UI":         ok,
		"absolute URL":  `[{"id":"a","context":"channel","title":"A","path":"https://evil.example/"}]`,
		"absolute path": `[{"id":"a","context":"channel","title":"A","path":"/api/plugins"}]`,
		"protocol rel.": `[{"id":"a","context":"channel","title":"A","path":"//evil.example"}]`,
		"script URL":    `[{"id":"a","context":"channel","title":"A","path":"javascript:alert(1)"}]`,
		"dot segments":  `[{"id":"a","context":"channel","title":"A","path":"../console/"}]`,
		"encoded dots":  `[{"id":"a","context":"channel","title":"A","path":"x/%2e%2e/%2E./console/"}]`,
		"empty title":   `[{"id":"a","context":"channel","title":" ","path":""}]`,
		"duplicate id":  `[{"id":"a","context":"channel","title":"A","path":""},{"id":"a","context":"channel","title":"B","path":""}]`,
	} {
		if err := load(bad, name != "no UI"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRotatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "x.log")
	r := NewRotatingFile(path, 100)
	line := []byte(strings.Repeat("a", 39) + "\n") // 40 bytes
	for i := 0; i < 6; i++ {
		_, _ = r.Write(line)
	}
	r.Close()
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	// Never more than the limit per file; the newest lines are in the current file.
	if len(cur) == 0 || len(cur) > 100 || len(old) == 0 || len(old) > 100 {
		t.Fatalf("sizes: current %d, old %d (limit 100)", len(cur), len(old))
	}
}

func TestDiagnosticsMasksSecrets(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "config"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "logs"), 0o755)
	// Unique strings: the bundle also lists every process of this machine.
	_ = os.WriteFile(filepath.Join(root, "config", "console.json"), []byte(`{"args":["--pin","pin-s3cr3t","--read-only","--webhook","https://hooks.example/arg-s3cr3t","--interval=2s","--name","name-s3cr3t"],`+
		`"env":{"MY_TOKEN":"token-s3cr3t","LEVEL":"3","HOOK":"https://user:pw-s3cr3t@hooks.example/env-s3cr3t?x=1","MIXER":"127.0.0.1:2345"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(root, "config", "rcplugd.env"), []byte("RCPLUG_ALLOW_HOST=studio\nAPI_KEY=key-s3cr3t\nWEBHOOK_URL=\"https://hooks.example/file-s3cr3t\"\n# a comment\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "logs", "sonar.log"), []byte("ok\npanic: boom\n"), 0o644)
	var buf bytes.Buffer
	if err := WriteDiagnostics(context.Background(), &buf, DiagnosticsInput{Root: root, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		all += "\n### " + f.Name + "\n" + string(b)
	}
	for _, secret := range []string{"pin-s3cr3t", "arg-s3cr3t", "name-s3cr3t", "token-s3cr3t", "pw-s3cr3t", "env-s3cr3t", "key-s3cr3t", "file-s3cr3t"} {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q leaked into the bundle", secret)
		}
	}
	for _, want := range []string{"--read-only", `"LEVEL": "3"`, "RCPLUG_ALLOW_HOST=studio", "panic: boom", "### rcplug.json",
		"--interval=2s", `"MIXER": "127.0.0.1:2345"`, "https://hooks.example/***", "WEBHOOK_URL=https://hooks.example/***", "# a comment"} {
		if !strings.Contains(all, want) {
			t.Errorf("bundle lacks %q", want)
		}
	}
}

func TestPortsSkipBusyOnes(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	root := t.TempDir()
	writePlugin(t, root, "web", "#!/bin/sh\nexit 0\n", `{"exec":["./run.sh"],"httpProxy":"127.0.0.1:{{port}}"}`)
	m := NewManager(root, port) // the first candidate is taken
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	if got := m.get("web").Port(); got == port || got == 0 {
		t.Fatalf("plugin got port %d, busy one is %d", got, port)
	}
}

// Starting one plugin must not freeze the autostart default of the others.
func TestStateRecordsOnlyUserChoices(t *testing.T) {
	root := t.TempDir()
	loop := "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n"
	writePlugin(t, root, "a", loop, `{"exec":["./run.sh"],"autostart":false}`)
	writePlugin(t, root, "b", loop, `{"exec":["./run.sh"],"autostart":true}`)
	m := NewManager(root, 9460)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	m.Enable("a")
	var ps persistState
	if err := sdk.LoadJSON(filepath.Join(root, "state.json"), &ps); err != nil {
		t.Fatal(err)
	}
	if len(ps.Enabled) != 1 || !ps.Enabled["a"] {
		t.Fatalf("state.json = %+v, want only a", ps.Enabled)
	}
}

func TestScanProblemsIgnoresPolicyRefusals(t *testing.T) {
	log := "console: write SYSTEM[15].systemName=\"My desk\" refused: control is off\nexited (exit status 1), restarting in 2s\n"
	got := scanProblems("console.log", []byte(log))
	if strings.Contains(got, "control is off") || !strings.Contains(got, "restarting in") {
		t.Fatal(got)
	}
}
