//go:build !linux

package hw

import "errors"

var errUnsupported = errors.New("only supported on Linux")

// Do is Linux-only.
func (p *Panel) Do(id string, value int) error { return errUnsupported }
