//go:build linux

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	bin := buildHost(t, dir)
	plugin := filepath.Join(dir, "root", "plugins", "trapper")
	os.MkdirAll(plugin, 0o755)
	os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"exec": ["/bin/sh", "./run.sh"], "autostart": true}`), 0o644)
	os.WriteFile(filepath.Join(plugin, "run.sh"), []byte("trap 'echo clean exit; exit 0' INT\necho running\nwhile :; do sleep 0.1; done\n"), 0o755)

	addr := freeAddr(t)
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

func buildHost(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "rcplugd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// When the host dies without stopping its plugins, helpers they started may
// survive; the next host removes them before it starts anything.
func TestLeftoversOfAKilledHostAreRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs rcplugd")
	}
	dir := t.TempDir()
	bin := buildHost(t, dir)
	root := filepath.Join(dir, "root")
	plugin := filepath.Join(root, "plugins", "spawner")
	os.MkdirAll(plugin, 0o755)
	os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"exec": ["/bin/sh", "./run.sh"], "autostart": true}`), 0o644)
	// The helper ignores SIGTERM, so the kernel's signal at the host's death does not end it.
	os.WriteFile(filepath.Join(plugin, "run.sh"), []byte(`sh -c 'trap "" INT TERM; echo $$ > "$RCPLUG_DATA_DIR/helper.pid"; exec sleep 1000' &
while :; do sleep 0.1; done
`), 0o755)
	pidFile := filepath.Join(root, "data", "spawner", "helper.pid")
	start := func() *exec.Cmd {
		h := exec.Command(bin, "--root", root, "--addr", freeAddr(t), "--updates=false")
		if err := h.Start(); err != nil {
			t.Fatal(err)
		}
		return h
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	alive := func(pid int) bool { return syscall.Kill(pid, 0) == nil }

	host := start()
	var helper int
	waitFor("the helper", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		helper, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && helper > 0
	})
	defer syscall.Kill(helper, syscall.SIGKILL)
	// The helper can be quicker than the host recording the plugin's process
	// group (under qemu-user it often is).
	waitFor("the process group to be recorded", func() bool {
		b, err := os.ReadFile(filepath.Join(root, ".groups"))
		return err == nil && strings.Contains(string(b), `"spawner"`)
	})
	host.Process.Kill() // like a crash: no plugin is stopped
	host.Wait()
	time.Sleep(300 * time.Millisecond)
	if !alive(helper) {
		t.Skip("the helper did not survive the host here; nothing to clean up")
	}

	host = start()
	defer func() { host.Process.Signal(syscall.SIGTERM); host.Wait() }()
	waitFor("the leftover helper to be removed", func() bool {
		// A zombie still answers kill 0 until it is reaped; look at its state.
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", helper))
		return err != nil || strings.Contains(string(b), ") Z ")
	})
}
