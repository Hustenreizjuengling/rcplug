//go:build linux

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// inputDevices describes the input devices: the kernel's list and, for every
// event device with position axes (a touchscreen), their ranges. It reads
// only; nothing is sent to a device.
func inputDevices() string {
	var b strings.Builder
	if raw, err := os.ReadFile("/proc/bus/input/devices"); err == nil {
		b.Write(raw)
	}
	nodes, _ := filepath.Glob("/dev/input/event*")
	sort.Strings(nodes)
	for _, n := range nodes {
		f, err := os.Open(n)
		if err != nil {
			continue
		}
		var axes []string
		for _, a := range []struct {
			code int
			name string
		}{{0x00, "ABS_X"}, {0x01, "ABS_Y"}, {0x2f, "ABS_MT_SLOT"}, {0x35, "ABS_MT_POSITION_X"}, {0x36, "ABS_MT_POSITION_Y"}, {0x39, "ABS_MT_TRACKING_ID"}} {
			if info, ok := absInfo(f.Fd(), a.code); ok {
				axes = append(axes, fmt.Sprintf("%s %d..%d", a.name, info[1], info[2]))
			}
		}
		f.Close()
		if len(axes) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", n, strings.Join(axes, ", "))
		}
	}
	return b.String()
}

// absInfo is EVIOCGABS: value, minimum, maximum, fuzz, flat, resolution.
func absInfo(fd uintptr, code int) ([6]int32, bool) {
	var info [6]int32
	req := uintptr(0x80184540 + code) // _IOR('E', 0x40 + code, struct input_absinfo)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&info))); e != 0 {
		return info, false
	}
	return info, info[2] > info[1] // a device without the axis reports 0..0
}
