//go:build linux

package host

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcessGroup starts the plugin in its own process group, so stopping it
// also reaches helpers it spawned (e.g. arecord), and asks the kernel to
// signal it if rcplugd dies. Helpers it started are not reached by that
// signal; the next rcplugd removes them (ReapLeftovers).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// interruptGroup sends SIGINT to the plugin's whole process group.
func interruptGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		return cmd.Process.Signal(syscall.SIGINT)
	}
	return nil
}

// killGroup removes whatever is left of the plugin's process group after the
// plugin itself exited (e.g. an arecord child that would keep the audio
// device busy). The group is gone already in the normal case (ESRCH).
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// killGroupIfIn kills the process group pgid if one of its members works in
// dir (its current directory is below it), and reports whether it did. The
// check keeps a process group number reused by something else safe.
func killGroupIfIn(pgid int, dir string) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(stat)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) < 3 || f[2] != strconv.Itoa(pgid) {
			continue
		}
		cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		if err == nil && strings.HasPrefix(cwd+"/", dir) {
			return syscall.Kill(-pgid, syscall.SIGKILL) == nil
		}
	}
	return false
}
