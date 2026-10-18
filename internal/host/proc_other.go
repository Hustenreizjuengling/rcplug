//go:build !linux

package host

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {}

func interruptGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

// killGroup is a no-op without process groups.
func killGroup(cmd *exec.Cmd) {}
