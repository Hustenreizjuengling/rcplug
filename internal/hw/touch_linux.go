//go:build linux

package hw

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Info finds the touchscreen (once): the given Device, else an event device
// with multi-touch position axes, a Goodix or "touch" one first.
func (t *Touch) Info() (TouchInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.infoLocked()
}

func (t *Touch) infoLocked() (TouchInfo, error) {
	if t.info != nil {
		return *t.info, nil
	}
	nodes := []string{t.Device}
	if t.Device == "" {
		nodes, _ = filepath.Glob("/dev/input/event*")
		sort.Strings(nodes)
	}
	var found []TouchInfo
	for _, n := range nodes {
		if info, ok := probeTouch(n); ok {
			found = append(found, info)
		}
	}
	if len(found) == 0 {
		return TouchInfo{}, ErrNoTouchscreen
	}
	best := found[0]
	for _, f := range found {
		name := strings.ToLower(f.Name)
		if f.MT && (strings.Contains(name, "goodix") || strings.Contains(name, "touch")) {
			best = f
			break
		}
		if f.MT && !best.MT {
			best = f
		}
	}
	t.info = &best
	return best, nil
}

func probeTouch(node string) (TouchInfo, bool) {
	f, err := os.Open(node)
	if err != nil {
		return TouchInfo{}, false
	}
	defer f.Close()
	info := TouchInfo{Device: node}
	if x, ok := absRange(f.Fd(), absMTPosX); ok {
		if y, ok := absRange(f.Fd(), absMTPosY); ok {
			info.XMin, info.XMax, info.YMin, info.YMax, info.MT = x[0], x[1], y[0], y[1], true
		}
	}
	if !info.MT {
		x, ok1 := absRange(f.Fd(), absX)
		y, ok2 := absRange(f.Fd(), absY)
		if !ok1 || !ok2 {
			return TouchInfo{}, false
		}
		info.XMin, info.XMax, info.YMin, info.YMax = x[0], x[1], y[0], y[1]
	}
	raw, _ := os.ReadFile(filepath.Join("/sys/class/input", filepath.Base(node), "device", "name"))
	info.Name = strings.TrimSpace(string(raw))
	return info, true
}

// absRange is EVIOCGABS: the axis' minimum and maximum.
func absRange(fd uintptr, code int) ([2]int32, bool) {
	var info [6]int32
	req := uintptr(0x80184540 + code) // _IOR('E', 0x40 + code, struct input_absinfo)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&info))); e != 0 {
		return [2]int32{}, false
	}
	return [2]int32{info[1], info[2]}, info[2] > info[1]
}

// Do performs a gesture on the touchscreen.
func (t *Touch) Do(g Gesture) error {
	if len(g.Points) == 0 {
		return fmt.Errorf("no point")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	info, err := t.infoLocked()
	if err != nil {
		return err
	}
	pts := g.steps()
	var pos [][2]int32
	for _, p := range pts {
		x, y := info.position(p, g.Portrait, g.Flip)
		pos = append(pos, [2]int32{x, y})
	}
	t.id = (t.id + 1) & 0xffff
	frames := info.frames(pos, 1000+t.id)
	f, err := os.OpenFile(info.Device, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	pause := 16 * time.Millisecond
	if len(pts) == 1 {
		pause = g.Duration
		if pause <= 0 {
			pause = 60 * time.Millisecond
		}
	}
	for k, fr := range frames {
		if _, err := f.Write(encode(fr)); err != nil {
			return err
		}
		if k < len(frames)-1 {
			time.Sleep(pause)
		}
	}
	return nil
}
