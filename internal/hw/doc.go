// Package hw gives the console the device hardware it uses besides the
// mixer's control port: the levels and fader positions in the mixer's meter
// memory (read-only) and, opt-in, emulation of front-panel inputs.
//
// Both are Linux-only; on other systems the functions return errors so
// development builds still compile.
package hw
