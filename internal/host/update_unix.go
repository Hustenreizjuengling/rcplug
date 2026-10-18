//go:build !windows

package host

import (
	"os"
	"os/exec"
	"syscall"
)

// installerScript runs a new device package's install.sh, records its exit
// status and the time in the result file, and removes the unpacked package
// and the then empty .update directory.
// install.sh stops this rcplugd, replaces it and starts the new version (or
// the previous one again if that fails).
const installerScript = `sh "$1/install.sh"; rc=$?
printf '%s %s\n' "$rc" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$3"
rm -rf "$1"; rmdir "${1%/*}" 2>/dev/null
exit $rc`

// spawnInstaller starts the installer in its own session, so it keeps
// running when install.sh stops this process.
func spawnInstaller(payload, logFile, resultFile string) error {
	out, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command("/bin/sh", "-c", installerScript, "rcplug-update", payload, logFile, resultFile)
	cmd.Stdout, cmd.Stderr, cmd.Dir = out, out, "/"
	cmd.Env = append(os.Environ(), "RCPLUG_STEPS=1") // install.sh logs each step with time and free memory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	setIdleIO(cmd.Process.Pid) // its copies go easy on the storage (see pace.go)
	go cmd.Wait()              // reap it if this process is still alive when it ends
	return nil
}

// smallReceiveBuffer caps an update connection's receive buffer at 64 KB, so
// the network delivers a download in small pieces (see pace.go).
func smallReceiveBuffer(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 64<<10)
	})
}
