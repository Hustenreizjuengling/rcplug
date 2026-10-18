//go:build !linux

package host

func idleIO()           {}
func setIdleIO(pid int) {}
