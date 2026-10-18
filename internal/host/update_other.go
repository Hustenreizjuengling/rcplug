//go:build windows

package host

import "errors"

// spawnInstaller is only available where the device runs (Linux); a Windows
// build of rcplugd is for development.
func spawnInstaller(payload, logFile, resultFile string) error {
	return errors.New("installing a device package is not supported on this system")
}
