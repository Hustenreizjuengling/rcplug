package sdk

import (
	"os"
	"strconv"
	"syscall"
)

// LowPriority moves every thread of this process to the lowest CPU priority
// (nice 19), so audio on the device always wins. On Linux, setpriority with
// PRIO_PROCESS affects a single thread; Go runs goroutines (and forks child
// processes) from several OS threads, so each existing thread is lowered.
// Threads created later inherit the value from their (already lowered)
// creator, and so do child processes.
func LowPriority() error {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19)
	}
	var firstErr error
	for _, t := range tasks {
		tid, err := strconv.Atoi(t.Name())
		if err != nil {
			continue
		}
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, 19); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
