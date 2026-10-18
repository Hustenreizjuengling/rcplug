//go:build !linux

package hw

import (
	"errors"
	"image"
)

var errUnsupported = errors.New("only supported on Linux")

// Capture is Linux-only.
func Capture(dev string) (image.Image, *FBInfo, error) { return nil, nil, errUnsupported }

// DRMInfo describes a captured scanout buffer (Linux only).
type DRMInfo struct {
	Device, Format, Via  string
	Width, Height, Pitch int
}

// CaptureDRM is Linux-only.
func CaptureDRM(dev string) (image.Image, *DRMInfo, error) { return nil, nil, errUnsupported }

// Do is Linux-only.
func (p *Panel) Do(id string, value int) error { return errUnsupported }

// Info is Linux-only.
func (t *Touch) Info() (TouchInfo, error) { return TouchInfo{}, errUnsupported }

// Do is Linux-only.
func (t *Touch) Do(g Gesture) error { return errUnsupported }
