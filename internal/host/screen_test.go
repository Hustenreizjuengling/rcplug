package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// standIn starts a copy of this test binary named comm that only sleeps
// (see init in services_test.go), with extra words on its command line.
func standIn(t *testing.T, comm string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	bin := filepath.Join(t.TempDir(), comm)
	b, _ := os.ReadFile(self)
	os.WriteFile(bin, b, 0o755)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "SVCTEST_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd
}

// testScreen is a Screen whose "RØDE screen" is made of stand-ins; starting
// the display service is recorded.
type testScreen struct {
	*Screen
	mu     sync.Mutex
	starts int
}

func newTestScreen(t *testing.T) *testScreen {
	dir := t.TempDir()
	ts := &testScreen{}
	ts.Screen = &Screen{Marker: filepath.Join(dir, "rcplug-screen"), Service: filepath.Join(dir, "rode-display-service"),
		Loop: "scrtest-display-start.sh", App: "scrtest-gui", procRoot: "/proc", logf: t.Logf,
		start: func(string) error { ts.mu.Lock(); ts.starts++; ts.mu.Unlock(); return nil }}
	os.WriteFile(ts.Service, []byte("#!/bin/sh\n"), 0o755)
	return ts
}

func (ts *testScreen) started() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.starts
}

func alive(cmd *exec.Cmd) bool {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "stat"))
	return err == nil && !strings.Contains(string(b), ") Z ")
}

// Taking the screen stops RØDE's loop and screen program; giving it back
// starts the display service, once, and removes the marker.
func TestScreenTakeAndRelease(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	ts := newTestScreen(t)
	loop := standIn(t, "scrtest-sh", "/usr/bin/scrtest-display-start.sh", "-s")
	gui := standIn(t, "scrtest-gui")
	waitFor(t, "stand-ins", 3*time.Second, func() bool { return alive(loop) && alive(gui) })

	if err := ts.Take("demo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "RØDE's screen stopped", 5*time.Second, func() bool { return !alive(loop) && !alive(gui) })
	if b, _ := os.ReadFile(ts.Marker); strings.TrimSpace(string(b)) != "demo" || ts.Holder() != "demo" {
		t.Fatalf("marker %q, holder %q", b, ts.Holder())
	}
	if err := ts.Take("other"); err != errScreenTaken {
		t.Fatalf("a second plugin: %v", err)
	}
	ts.Release("other") // not its screen
	if ts.Holder() != "demo" || ts.started() != 0 {
		t.Fatal("released by someone else")
	}
	ts.Release("demo")
	if _, err := os.Stat(ts.Marker); !os.IsNotExist(err) || ts.started() != 1 || ts.Holder() != "" {
		t.Fatalf("after release: marker %v, starts %d", err, ts.started())
	}
	ts.Release("") // nothing left to do
	if ts.started() != 1 {
		t.Fatal("started twice")
	}
}

// A marker left by an earlier rcplugd (it died with a plugin on the screen)
// hands the screen back at the next start.
func TestScreenRecovers(t *testing.T) {
	ts := newTestScreen(t)
	os.WriteFile(ts.Marker, []byte("demo\n"), 0o644)
	ts.Release("")
	if _, err := os.Stat(ts.Marker); !os.IsNotExist(err) || ts.started() != 1 {
		t.Fatalf("not recovered: %v, starts %d", err, ts.started())
	}
}

// A plugin with the screen: never started by Load, takes the screen when the
// dashboard starts it, runs once, and the screen comes back however it ends.
func TestScreenPluginLifecycle(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "screendemo",
		"#!/bin/sh\nif [ -f \"$RCPLUG_DATA_DIR/end\" ]; then rm -f \"$RCPLUG_DATA_DIR/end\"; exit 0; fi\ntrap 'exit 0' INT TERM\nwhile [ ! -f \"$RCPLUG_DATA_DIR/end\" ]; do sleep 0.1; done\nrm -f \"$RCPLUG_DATA_DIR/end\"\n",
		`{"name":"screendemo","title":"Demo","version":"1.0","exec":["./run.sh"],"permissions":["screen"]}`)
	os.WriteFile(filepath.Join(root, "state.json"), []byte(`{"enabled": {"screendemo": true}}`), 0o644)
	ts := newTestScreen(t)
	m := NewManager(root, 9300)
	m.Screen = ts.Screen
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if s, _ := statusOf(m, "screendemo"); s.Running || ts.Holder() != "" {
		t.Fatalf("started by itself: %+v", s)
	}

	// Started from the dashboard; it ends by itself ("back to RØDE").
	m.Enable("screendemo")
	waitFor(t, "running with the screen", 3*time.Second, func() bool {
		s, _ := statusOf(m, "screendemo")
		return s.Running && ts.Holder() == "screendemo"
	})
	data := filepath.Join(root, "data", "screendemo")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "end"), nil, 0o644)
	defer func() {
		if t.Failed() {
			s, _ := statusOf(m, "screendemo")
			t.Logf("status %+v holder %q starts %d logs %q", s, ts.Holder(), ts.started(), m.Logs("screendemo"))
		}
	}()
	waitFor(t, "screen back after the plugin ended", 5*time.Second, func() bool {
		s, _ := statusOf(m, "screendemo")
		return s.State == StateStopped && ts.Holder() == "" && ts.started() == 1
	})
	time.Sleep(1500 * time.Millisecond) // past a restart's backoff
	if s, _ := statusOf(m, "screendemo"); s.Running || s.Restarts != 0 {
		t.Fatalf("started again: %+v", s)
	}

	// Started again and stopped from the dashboard.
	m.Enable("screendemo")
	waitFor(t, "running again", 3*time.Second, func() bool { s, _ := statusOf(m, "screendemo"); return s.Running })
	m.Disable("screendemo")
	if ts.Holder() != "" || ts.started() != 2 {
		t.Fatalf("after stop: holder %q, starts %d", ts.Holder(), ts.started())
	}
	// Nothing about it is remembered for the next start of rcplug.
	if b, _ := os.ReadFile(filepath.Join(root, "state.json")); strings.Contains(string(b), `"screendemo": true`) && !strings.Contains(string(b), `"screendemo": false`) {
		t.Fatalf("state.json remembers the screen plugin as running: %s", b)
	}

	// Another plugin cannot take the screen while one has it.
	m.Enable("screendemo")
	waitFor(t, "running a third time", 3*time.Second, func() bool { s, _ := statusOf(m, "screendemo"); return s.Running })
	if err := ts.Take("other"); err != errScreenTaken {
		t.Fatalf("second holder: %v", err)
	}
	m.StopAll()
	if ts.Holder() != "" {
		t.Fatal("rcplug stopping left the screen away")
	}
}

func TestScreenPluginNeverAutostarts(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "bad", "#!/bin/sh\n", `{"name":"bad","version":"1","exec":["./run.sh"],"autostart":true,"permissions":["screen"]}`)
	if _, err := LoadManifest(filepath.Join(root, "plugins", "bad")); err == nil || !strings.Contains(err.Error(), "never starts by itself") {
		t.Fatalf("manifest: %v", err)
	}
}
