//go:build windows

package host

import (
	"errors"
	"syscall"
)

// spawnInstaller is only available where the device runs (Linux); a Windows
// build of rcplugd is for development.
func spawnInstaller(payload, logFile, resultFile string) error {
	return errors.New("installing a device package is not supported on this system")
}

// smallReceiveBuffer only matters on the device.
func smallReceiveBuffer(network, address string, c syscall.RawConn) error { return nil }

// spawnDetached is only available where the device runs (Linux).
func spawnDetached(script string, args ...string) error {
	return errors.New("not supported on this system")
}
