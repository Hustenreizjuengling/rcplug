//go:build linux

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A host started with SIGINT ignored (as a background job of a shell is)
// must still start plugins with SIGINT's default action, or they cannot
// trap it and are killed instead of stopping cleanly.
func TestPluginsGetSIGINTEvenIfHostIgnoresIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs rcplugd")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "rcplugd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	plugin := filepath.Join(dir, "root", "plugins", "trapper")
	os.MkdirAll(plugin, 0o755)
	os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"exec": ["/bin/sh", "./run.sh"], "autostart": true}`), 0o644)
	os.WriteFile(filepath.Join(plugin, "run.sh"), []byte("trap 'echo clean exit; exit 0' INT\necho running\nwhile :; do sleep 0.1; done\n"), 0o755)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	host := exec.Command("/bin/sh", "-c", `trap '' INT; exec "$0" --root "$1" --addr "$2" --updates=false`,
		bin, filepath.Join(dir, "root"), addr)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { host.Process.Kill(); host.Wait() }()

	logFile := filepath.Join(dir, "root", "logs", "trapper.log")
	waitLog := func(what string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if b, _ := os.ReadFile(logFile); strings.Contains(string(b), what) {
				return
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(logFile)
				t.Fatalf("no %q in the plugin log:\n%s", what, b)
			}
		}
	}
	waitLog("running")
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/api/plugins/trapper/disable", addr), nil)
	req.Header.Set("X-Rcplug", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitLog("clean exit")
}
