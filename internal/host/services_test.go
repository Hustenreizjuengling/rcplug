package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A copy of this test binary started with SVCTEST_SLEEP only sleeps: the
// stand-in for a service's process.
func init() {
	if os.Getenv("SVCTEST_SLEEP") != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// fakeMounter records bind mounts instead of making them.
type fakeMounter struct {
	mu     sync.Mutex
	mounts map[string]string // dst → src
	fail   error
}

func (f *fakeMounter) bind(src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if f.mounts == nil {
		f.mounts = map[string]string{}
	}
	f.mounts[dst] = src
	return nil
}

func (f *fakeMounter) unbind(dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.mounts, dst)
	return nil
}

func (f *fakeMounter) mounted(dst string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.mounts[dst]
	return ok
}

type svcRig struct {
	s       *Services
	mnt     *fakeMounter
	root    string
	device  string
	ran     []string
	started []string
}

// newSvcRig sets up a device folder with RØDE's files as given (the
// catalog knows their sums) and rcplug's root next to it.
func newSvcRig(t *testing.T, catalog []Service, files map[string]string) *svcRig {
	t.Helper()
	dir := t.TempDir()
	r := &svcRig{mnt: &fakeMounter{}, root: filepath.Join(dir, "rcplug"), device: filepath.Join(dir, "device")}
	os.MkdirAll(r.root, 0o755)
	for i := range catalog {
		body := files[catalog[i].Path]
		if body == "" {
			continue
		}
		p := filepath.Join(r.device, catalog[i].Path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
		sum := sha256.Sum256([]byte(body))
		catalog[i].Known = append(catalog[i].Known, hex.EncodeToString(sum[:]))
	}
	r.s = &Services{root: r.root, Prefix: r.device, procRoot: "/proc", catalog: catalog, mnt: r.mnt, logf: t.Logf,
		run: func(ctx context.Context, script, arg string) error {
			r.ran = append(r.ran, filepath.Base(script)+" "+arg)
			return nil
		},
		start: func(path string, script bool) error {
			r.started = append(r.started, filepath.Base(path))
			return nil
		}}
	return r
}

func (r *svcRig) state(t *testing.T) persistState {
	t.Helper()
	var ps persistState
	b, _ := os.ReadFile(filepath.Join(r.root, "state.json"))
	_ = json.Unmarshal(b, &ps)
	return ps
}

// testCatalog has the real paths but made-up process names: a test must
// never stop a process of the machine it runs on.
func testCatalog() []Service {
	return []Service{
		{ID: "callme", Path: "/etc/init.d/callme-t-service", Script: true, Procs: []string{"svctest-none-1"}},
		{ID: "modemmanager", Path: "/usr/sbin/ModemManager", Procs: []string{"svctest-none-2"}},
	}
}

func TestServiceOffAndOn(t *testing.T) {
	r := newSvcRig(t, testCatalog(), map[string]string{
		"/etc/init.d/callme-t-service": "#!/bin/sh\necho callme\n", "/usr/sbin/ModemManager": "ELF",
	})
	// Other choices in state.json stay as they are.
	os.WriteFile(filepath.Join(r.root, "state.json"), []byte(`{"enabled": {"sonar": true}}`), 0o644)

	if err := r.s.Off(context.Background(), "callme"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(r.device, "/etc/init.d/callme-t-service")
	if src := r.mnt.mounts[dst]; src != filepath.Join(r.root, "lib", "service-off") {
		t.Fatalf("mounted %q over the script", src)
	}
	if b, _ := os.ReadFile(filepath.Join(r.root, "lib", "service-off")); !strings.Contains(string(b), "switched off by rcplug") {
		t.Fatalf("off script %q", b)
	}
	if strings.Join(r.ran, ",") != "callme-t-service stop" {
		t.Fatalf("ran %v: the original script stops what runs, before it is covered", r.ran)
	}
	if st := r.state(t); strings.Join(st.ServicesOff, ",") != "callme" || !st.Enabled["sonar"] {
		t.Fatalf("state %+v", st)
	}
	// Off again: nothing new happens.
	if err := r.s.Off(context.Background(), "callme"); err != nil || len(r.ran) != 1 {
		t.Fatalf("again: %v, ran %v", err, r.ran)
	}

	if err := r.s.On("callme"); err != nil {
		t.Fatal(err)
	}
	if r.mnt.mounted(dst) || len(r.state(t).ServicesOff) != 0 || strings.Join(r.started, ",") != "callme-t-service" {
		t.Fatalf("on: mounted %v, state %+v, started %v", r.mnt.mounted(dst), r.state(t), r.started)
	}

	// A program D-Bus starts: covered, nothing run to stop it.
	if err := r.s.Off(context.Background(), "modemmanager"); err != nil || len(r.ran) != 1 {
		t.Fatalf("modemmanager: %v, ran %v", err, r.ran)
	}
	if err := r.s.Off(context.Background(), "nothing"); err != errUnknownService {
		t.Fatalf("unknown service: %v", err)
	}
}

// A file rcplug does not know (another firmware) is never covered.
func TestServiceUnknownVersionStaysOn(t *testing.T) {
	r := newSvcRig(t, testCatalog(), map[string]string{"/etc/init.d/callme-t-service": "#!/bin/sh\necho callme\n"})
	os.WriteFile(filepath.Join(r.device, "/etc/init.d/callme-t-service"), []byte("#!/bin/sh\necho changed by a new firmware\n"), 0o755)
	if err := r.s.Off(context.Background(), "callme"); err != errServiceVersion {
		t.Fatalf("unknown version: %v", err)
	}
	if err := r.s.Off(context.Background(), "modemmanager"); err != errServiceMissing {
		t.Fatalf("missing: %v", err)
	}
	if len(r.mnt.mounts) != 0 || len(r.ran) != 0 || len(r.state(t).ServicesOff) != 0 {
		t.Fatalf("something happened: %v %v %+v", r.mnt.mounts, r.ran, r.state(t))
	}
	st := r.s.Status(procSnapshot{})
	if st[0].State != "unavailable" || st[0].Reason != "unknown_version" || st[1].Reason != "not_present" {
		t.Fatalf("status %+v", st)
	}
}

// At boot the choices are applied before RØDE's services start; on
// uninstall everything is undone and started again.
func TestServicesApplyAndRestore(t *testing.T) {
	r := newSvcRig(t, testCatalog(), map[string]string{
		"/etc/init.d/callme-t-service": "#!/bin/sh\necho callme\n", "/usr/sbin/ModemManager": "ELF",
	})
	os.WriteFile(filepath.Join(r.root, "state.json"), []byte(`{"enabled": {}, "servicesOff": ["callme", "modemmanager", "gone"]}`), 0o644)
	if err := r.s.Apply(); err != nil {
		t.Fatal(err)
	}
	if len(r.mnt.mounts) != 2 || len(r.ran) != 0 || len(r.started) != 0 {
		t.Fatalf("apply: mounts %v, ran %v, started %v", r.mnt.mounts, r.ran, r.started)
	}
	if st := r.s.Status(procSnapshot{}); st[0].State != "off" || st[1].State != "off" {
		t.Fatalf("status after apply %+v", st)
	}
	if err := r.s.Apply(); err != nil || len(r.mnt.mounts) != 2 {
		t.Fatalf("apply again: %v", err)
	}
	if err := r.s.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(r.mnt.mounts) != 0 || strings.Join(r.started, ",") != "callme-t-service,ModemManager" {
		t.Fatalf("restore: mounts %v, started %v", r.mnt.mounts, r.started)
	}

	// Another firmware's file at boot: left on, and said why.
	os.WriteFile(filepath.Join(r.device, "/etc/init.d/callme-t-service"), []byte("other"), 0o755)
	err := r.s.Apply()
	if err == nil || !strings.Contains(err.Error(), "callme: unknown_version") || r.mnt.mounted(filepath.Join(r.device, "/etc/init.d/callme-t-service")) {
		t.Fatalf("apply with another firmware: %v", err)
	}
	if st := r.s.Status(procSnapshot{}); st[0].State != "unavailable" {
		t.Fatalf("status %+v", st)
	}
}

// Switching off stops what runs, also a process that ignores the script.
func TestServiceOffStopsItsProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	// This test binary under another name stands in for a service's process
	// (it only sleeps, see init).
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "svctest-proc")
	b, _ := os.ReadFile(self)
	os.WriteFile(bin, b, 0o755)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "SVCTEST_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	cat := []Service{{ID: "callme", Path: "/etc/init.d/callme-t-service", Script: true, Procs: []string{"svctest-proc"}}}
	r := newSvcRig(t, cat, map[string]string{"/etc/init.d/callme-t-service": "#!/bin/sh\n"})
	for deadline := time.Now().Add(3 * time.Second); r.s.running(cat[0].Procs) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("test process not found")
		}
	}
	snap := (&procSampler{}).sample("/proc", nil, os.Getpid())
	if st := r.s.Status(snap); len(st[0].Procs) != 1 || st[0].Procs[0].Name != "svctest-proc" {
		t.Fatalf("status procs %+v", st[0].Procs)
	}
	if err := r.s.Off(context.Background(), "callme"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the service's process still runs")
	}
}

// The real thing: a bind mount over a script, and back (root only; make
// test-root).
func TestServiceBindMountAsRoot(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	r := newSvcRig(t, testCatalog(), map[string]string{"/etc/init.d/callme-t-service": "#!/bin/sh\necho callme runs\n"})
	r.s.mnt = sysMounter{}
	script := filepath.Join(r.device, "/etc/init.d/callme-t-service")
	t.Cleanup(func() { _ = sysMounter{}.unbind(script) })
	if err := r.s.Off(context.Background(), "callme"); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("/bin/sh", script, "start").CombinedOutput()
	if !strings.Contains(string(out), "switched off by rcplug") || !(sysMounter{}).mounted(script) {
		t.Fatalf("covered script says %q", out)
	}
	if err := r.s.On("callme"); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("/bin/sh", script, "start").CombinedOutput()
	if strings.TrimSpace(string(out)) != "callme runs" {
		t.Fatalf("after switching on the script says %q", out)
	}
}

// The API: every service with its state, switched off and on over HTTP.
func TestServicesAPI(t *testing.T) {
	r := newSvcRig(t, testCatalog(), map[string]string{"/etc/init.d/callme-t-service": "#!/bin/sh\n"})
	m := NewManager(r.root, 9400)
	srv := NewServer(m, nil, "test")
	srv.Services = r.s
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	type answer struct {
		Services []ServiceStatus `json:"services"`
		Cores    int             `json:"cores"`
		Code     string          `json:"code"`
	}
	call := func(method, path, body string) (int, answer) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Rcplug", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var a answer
		_ = json.NewDecoder(res.Body).Decode(&a)
		return res.StatusCode, a
	}
	code, a := call("GET", "/api/services", "")
	if code != 200 || len(a.Services) != 2 || a.Services[0].State != "on" || a.Services[1].State != "unavailable" || a.Cores < 1 {
		t.Fatalf("list: %d %+v", code, a)
	}
	if code, a = call("POST", "/api/services/callme", `{"on": false}`); code != 200 || a.Services[0].State != "off" {
		t.Fatalf("off: %d %+v", code, a)
	}
	if code, a = call("POST", "/api/services/callme", `{"on": true}`); code != 200 || a.Services[0].State != "on" {
		t.Fatalf("on: %d %+v", code, a)
	}
	if code, a = call("POST", "/api/services/modemmanager", `{"on": false}`); code != 409 || a.Code != "not_present" {
		t.Fatalf("not present: %d %+v", code, a)
	}
	if code, _ = call("POST", "/api/services/callme", `{}`); code != 400 {
		t.Fatalf("no choice: %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/services/callme", strings.NewReader(`{"on": false}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Fatalf("without X-Rcplug: %v %v", err, res.StatusCode)
	}
}

// A backup carries which RØDE services are off; restoring it switches them
// so. A backup from before (no field) leaves them alone. A plugin with the
// screen is in it like any other (its state is remembered since 0.8.17).
func TestServicesInBackups(t *testing.T) {
	r := newSvcRig(t, testCatalog(), map[string]string{"/etc/init.d/callme-t-service": "#!/bin/sh\n"})
	writePlugin(t, r.root, "screendemo", "#!/bin/sh\nsleep 30\n", `{"name":"screendemo","version":"1","exec":["./run.sh"],"permissions":["screen"]}`)
	m := NewManager(r.root, 9500)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	b := m.Backup("test")
	if b.ServicesOff == nil || len(*b.ServicesOff) != 0 {
		t.Fatalf("backup services %v", b.ServicesOff)
	}
	for _, p := range b.Plugins {
		if p.Name == "screendemo" && (p.Enabled == nil || *p.Enabled) {
			t.Fatalf("a screen plugin's running state should be in the backup, off: %+v", p)
		}
	}

	off := []string{"callme", "teleporter"}
	b.ServicesOff = &off
	on := true
	b.Plugins = []BackupPlugin{{Name: "screendemo", Version: "1", Enabled: &on}}
	plan := m.plan(b)
	var changes []string
	for _, c := range plan.Changes {
		changes = append(changes, c.What+" "+c.ID+" "+string(c.To))
	}
	// The screen plugin's state is restored like any other's (this manager
	// has no screen controller, so the start itself is refused here).
	if strings.Join(changes, ",") != "enabled  true,service callme false" || len(plan.Skipped) != 1 || plan.Skipped[0].ID != "teleporter" {
		t.Fatalf("plan %v, skipped %+v", changes, plan.Skipped)
	}
	if _, errs := m.restore(plan, r.s); len(errs) != 0 {
		t.Fatal(errs)
	}
	if !r.mnt.mounted(filepath.Join(r.device, "/etc/init.d/callme-t-service")) {
		t.Fatal("restoring did not switch CallMe off")
	}
	if s, _ := statusOf(m, "screendemo"); s.Running {
		t.Fatal("the screen plugin ran without a screen controller")
	}

	b.ServicesOff = nil // a backup from before
	for _, c := range m.plan(b).Changes {
		if c.What == "service" {
			t.Fatalf("an old backup changes services: %+v", c)
		}
	}
	m.StopAll()
}
