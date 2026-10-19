//go:build !linux

package host

import "errors"

// sysMounter: switching services off needs Linux bind mounts.
type sysMounter struct{}

func (sysMounter) bind(src, dst string) error { return errors.New("not supported on this system") }
func (sysMounter) unbind(dst string) error    { return errors.New("not supported on this system") }
func (sysMounter) mounted(dst string) bool    { return false }
