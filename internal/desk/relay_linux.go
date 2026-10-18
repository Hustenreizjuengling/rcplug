//go:build linux

package desk

import (
	"net"
	"syscall"
)

// keepAbove: when a process ends, even by a crash, the kernel closes its
// descriptors in ascending order. A duplicate of the mixer connection above
// every other descriptor keeps that connection open until the listener on
// the mixer's port (a low descriptor) is gone, so the mixer, which listens
// again the moment its client leaves, never finds its port taken.
var keepAbove = []int{900, 500, 200}

func keepHigh(conn net.Conn) (int, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return -1, syscall.EINVAL
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return -1, err
	}
	fd, dupErr := -1, error(syscall.EINVAL)
	err = raw.Control(func(f uintptr) {
		for _, min := range keepAbove {
			r, _, e := syscall.Syscall(syscall.SYS_FCNTL, f, syscall.F_DUPFD_CLOEXEC, uintptr(min))
			if e == 0 {
				fd, dupErr = int(r), nil
				return
			}
			dupErr = e
		}
	})
	if err != nil {
		return -1, err
	}
	return fd, dupErr
}

func closeKeep(fd int) {
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}
