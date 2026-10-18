//go:build !linux

package desk

import "net"

// keepHigh: the crash-safe descriptor order is only needed (and done) on the
// device.
func keepHigh(conn net.Conn) (int, error) { return -1, nil }

func closeKeep(fd int) {}
