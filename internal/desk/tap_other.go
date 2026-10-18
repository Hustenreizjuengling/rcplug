//go:build !linux

package desk

import "errors"

type capture struct{}

func openCapture(port int) (*capture, error) {
	return nil, errors.New("reading along is only possible on Linux")
}

func (c *capture) next() ([]byte, error) { return nil, errors.New("closed") }
func (c *capture) close()                {}

// RestartScreen is only possible on the device.
func RestartScreen() error {
	return errors.New("restarting the device's screen is only possible on the device")
}
