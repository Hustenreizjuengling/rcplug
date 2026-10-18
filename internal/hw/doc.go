// Package hw gives the console optional, read-mostly access to the desk's
// hardware: a screenshot of the touchscreen (legacy framebuffer or DRM/KMS
// scanout buffer) and, opt-in, emulation of front-panel inputs.
//
// Capture and input injection are Linux-only; on other systems the functions
// return errors so development builds still compile.
package hw
