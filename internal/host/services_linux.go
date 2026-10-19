//go:build linux

package host

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// sysMounter makes real bind mounts (rcplugd runs as root on the device).
type sysMounter struct{}

func (sysMounter) bind(src, dst string) error {
	return syscall.Mount(src, dst, "", syscall.MS_BIND, "")
}

func (sysMounter) unbind(dst string) error {
	if err := syscall.Unmount(dst, 0); err != nil {
		// A script that runs right now keeps the file busy: let go of it
		// for new users at once, the kernel finishes when it ends.
		return syscall.Unmount(dst, syscall.MNT_DETACH)
	}
	return nil
}

// mounted reports whether something is mounted on dst (/proc/self/mountinfo,
// where a space in a path is written \040).
func (sysMounter) mounted(dst string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 && unescapeMount(fields[4]) == dst {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			n := 0
			ok := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				n = n*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
