//go:build linux

package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A helper process that ignores the stop signal must not outlive its plugin
// (think of an arecord child holding the audio device).
func TestStopKillsLeftoverChildren(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	// The child ignores SIGINT, so only the cleanup of the group removes it.
	writePlugin(t, root, "stubborn", `#!/bin/sh
sh -c 'trap "" INT; echo $$ > `+pidFile+`; while :; do sleep 1; done' &
trap 'exit 0' INT TERM
while :; do sleep 1; done
`, `{"exec":["./run.sh"],"autostart":true}`)
	m := NewManager(root, 0)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	var child int
	waitFor(t, "child pid", 5*time.Second, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	m.Disable("stubborn")
	waitFor(t, "child to be gone", 5*time.Second, func() bool {
		return syscall.Kill(child, 0) == syscall.ESRCH || isZombie(child)
	})
}

func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] == "Z"
}
