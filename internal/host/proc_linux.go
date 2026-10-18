//go:build linux

package host

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the plugin in its own process group, so stopping it
// also reaches helpers it spawned (e.g. arecord), and asks the kernel to
// signal it if rcplugd dies, so no orphan keeps its port after a crash.
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
