//go:build !linux

package sdk

// LowPriority is a no-op outside Linux (development builds on other systems).
func LowPriority() error { return nil }
