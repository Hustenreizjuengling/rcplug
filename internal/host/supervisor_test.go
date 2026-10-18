package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePlugin creates plugins/<name>/ with a plugin.json and a shell script.
func writePlugin(t *testing.T, root, name, script, manifest string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sh := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(sh, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(m *Manager, name string) (Status, bool) {
	for _, s := range m.List() {
		if s.Name == name {
			return s, true
		}
	}
	return Status{}, false
}

func TestSupervisorLifecycle(t *testing.T) {
	root := t.TempDir()
	// A long-lived plugin that logs a line and waits for a signal.
	writePlugin(t, root, "echoer",
		"#!/bin/sh\necho \"hello from $RCPLUG_NAME\"\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n",
		`{"name":"echoer","title":"Echoer","version":"1.0","exec":["./run.sh"],"autostart":true}`)

	m := NewManager(root, 9100)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "echoer running", 3*time.Second, func() bool {
		s, ok := statusOf(m, "echoer")
		return ok && s.Running && s.PID > 0
	})
	waitFor(t, "echoer log line", 2*time.Second, func() bool {
		for _, l := range m.Logs("echoer") {
			if strings.Contains(l, "hello from echoer") {
				return true
			}
		}
		return false
	})

	// Disable must stop it and persist state.
	m.Disable("echoer")
	waitFor(t, "echoer stopped", 3*time.Second, func() bool {
		s, _ := statusOf(m, "echoer")
		return s.State == StateStopped && !s.Running
	})
	if _, err := os.Stat(filepath.Join(root, "state.json")); err != nil {
		t.Fatalf("state.json not written: %v", err)
	}

	// Enable brings it back.
	m.Enable("echoer")
	waitFor(t, "echoer running again", 3*time.Second, func() bool {
		s, _ := statusOf(m, "echoer")
		return s.Running
	})
	m.StopAll()
}

func TestSupervisorRestartsCrashingPlugin(t *testing.T) {
	root := t.TempDir()
	// Exits immediately → supervisor must keep restarting it.
	writePlugin(t, root, "crasher",
		"#!/bin/sh\necho boom\nexit 1\n",
		`{"name":"crasher","title":"Crasher","version":"1.0","exec":["./run.sh"],"autostart":true}`)

	m := NewManager(root, 9100)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "crasher restarted at least twice", 5*time.Second, func() bool {
		s, _ := statusOf(m, "crasher")
		return s.Restarts >= 2
	})
	m.StopAll()
	// After StopAll the loop must be gone and not restart further.
	s, _ := statusOf(m, "crasher")
	after := s.Restarts
	time.Sleep(400 * time.Millisecond)
	s2, _ := statusOf(m, "crasher")
	if s2.Restarts > after+1 {
		t.Fatalf("plugin kept restarting after StopAll: %d -> %d", after, s2.Restarts)
	}
}

func TestLoadSkipsInvalidManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "broken")
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{ this is not json `), 0644)

	m := NewManager(root, 9100)
	invalid, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 || !strings.Contains(invalid[0], "broken") {
		t.Fatalf("expected one invalid plugin mentioning 'broken', got %v", invalid)
	}
	if len(m.List()) != 0 {
		t.Fatalf("invalid plugin should not be listed")
	}
}

func TestLineWriter(t *testing.T) {
	log := newRingLog(100, nil)
	w := &lineWriter{log: log}
	w.Write([]byte("2026/10/18 04:58:30 from Go's log\nplain line\n2026/10/18 04:58:30.123456 micro\n"))
	long := strings.Repeat("x", 2*maxLine+10)
	for i := 0; i < len(long); i += 32 << 10 { // arrives in pipe-sized chunks
		w.Write([]byte(long[i:min(i+32<<10, len(long))]))
	}
	w.Write([]byte("\n"))
	w.Write([]byte("no newline at the end"))
	w.flush()
	var got []string
	for _, l := range log.snapshot() {
		got = append(got, l[len("15:04:05 "):])
	}
	want := []string{"from Go's log", "plain line", "micro", strings.Repeat("x", maxLine), strings.Repeat("x", maxLine), "xxxxxxxxxx", "no newline at the end"}
	if len(got) != len(want) {
		t.Fatalf("%d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: %.40q…, want %.40q…", i, got[i], want[i])
		}
	}
}
