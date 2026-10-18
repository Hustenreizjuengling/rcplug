//go:build linux

package hw

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Do performs a gesture: for a button a press (value 0x00, 50 ms, 0x01 —
// the order rc2http uses on the Duo), for a wheel one detent (dir -1/+1), for
// a fader an absolute position 0..127.
func (p *Panel) Do(id string, value int) error {
	c, ok := findControl(id)
	if !ok {
		return fmt.Errorf("unknown control %q", id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// O_NOCTTY: the panel's tty must never become a controlling terminal.
	f, err := os.OpenFile(p.tty(), os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	switch c.Kind {
	case "button":
		if err := inject(f.Fd(), c.bytes(0x00)...); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		return inject(f.Fd(), c.bytes(0x01)...)
	case "wheel":
		v := byte(0x01)
		if value < 0 {
			v = 0x7f
		}
		return inject(f.Fd(), c.bytes(v)...)
	case "fader":
		if value < 0 || value > 127 {
			return fmt.Errorf("fader value must be 0..127")
		}
		return inject(f.Fd(), c.bytes(byte(value))...)
	}
	return fmt.Errorf("unsupported control kind %q", c.Kind)
}

// inject pushes one 3-byte message into the tty input queue.
func inject(fd uintptr, b ...byte) error {
	for i := range b {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSTI, uintptr(unsafe.Pointer(&b[i]))); e != 0 {
			return fmt.Errorf("TIOCSTI: %w", e)
		}
	}
	return nil
}
