//go:build linux

package host

import "syscall"

// ioprio_set(2): IOPRIO_WHO_PROCESS, class IDLE.
const (
	ioprioWhoProcess = 1
	ioprioIdle       = 3 << 13
)

// idleIO gives the calling thread the lowest I/O priority (best effort).
func idleIO() { setIdleIO(0) }

// setIdleIO gives a process (its main thread, and the children it starts
// from then on) the lowest I/O priority (best effort).
func setIdleIO(pid int) {
	_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(pid), ioprioIdle)
}
