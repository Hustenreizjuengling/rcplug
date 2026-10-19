//go:build linux

package desk

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// capture reads the IPv4 packets of one TCP port on the loopback interface:
// an AF_PACKET socket with a kernel filter, so only those packets are copied
// to us. It needs root (CAP_NET_RAW) and never sends anything.
type capture struct {
	fd  int
	buf []byte
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// tapFilter is a classic BPF program over the IP header (the socket delivers
// packets without a link header): TCP, not a fragment, source or destination
// port == port.
func tapFilter(port int) []syscall.SockFilter {
	p := uint32(port)
	return []syscall.SockFilter{
		{Code: 0x30, K: 9},                    // ldb [9]: protocol
		{Code: 0x15, Jt: 0, Jf: 8, K: 6},      // TCP, else drop
		{Code: 0x28, K: 6},                    // ldh [6]: flags and fragment offset
		{Code: 0x45, Jt: 6, Jf: 0, K: 0x1fff}, // a fragment: drop
		{Code: 0xb1, K: 0},                    // ldxb 4*([0]&0xf): IP header length
		{Code: 0x48, K: 0},                    // ldh [x+0]: source port
		{Code: 0x15, Jt: 2, Jf: 0, K: p},      // == port: accept
		{Code: 0x48, K: 2},                    // ldh [x+2]: destination port
		{Code: 0x15, Jt: 0, Jf: 1, K: p},      // == port: accept, else drop
		{Code: 0x06, K: 1 << 18},              // accept (up to 256 KiB)
		{Code: 0x06, K: 0},                    // drop
	}
}

func openCapture(port int) (*capture, error) {
	lo, err := loopback()
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_IP)))
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return nil, fmt.Errorf("reading along needs root (CAP_NET_RAW): %w", err)
		}
		return nil, fmt.Errorf("opening a packet socket: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			syscall.Close(fd)
		}
	}()
	if err := syscall.AttachLsf(fd, tapFilter(port)); err != nil {
		return nil, fmt.Errorf("attaching the packet filter: %w", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: lo.Index}); err != nil {
		return nil, fmt.Errorf("binding to %s: %w", lo.Name, err)
	}
	// Room for a full sync arriving at once; as root beyond rmem_max.
	if syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUFFORCE, 8<<20) != nil {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8<<20)
	}
	// Wake up regularly so Close is noticed.
	tv := syscall.NsecToTimeval(int64(500 * time.Millisecond))
	_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	ok = true
	return &capture{fd: fd, buf: make([]byte, 1<<18)}, nil
}

func loopback() (*net.Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for i := range ifs {
		if ifs[i].Flags&net.FlagLoopback != 0 {
			return &ifs[i], nil
		}
	}
	return nil, errors.New("no loopback interface")
}

// next returns the next packet (IP header first), nil after a read timeout.
// Each packet on lo passes the socket twice, outgoing and incoming; only the
// incoming copy is returned. The slice is valid until the next call.
func (c *capture) next() ([]byte, error) {
	n, from, err := syscall.Recvfrom(c.fd, c.buf, 0)
	if err != nil {
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			return nil, nil
		}
		return nil, os.NewSyscallError("recvfrom", err)
	}
	if ll, ok := from.(*syscall.SockaddrLinklayer); ok && ll.Pkttype == syscall.PACKET_OUTGOING {
		return nil, nil
	}
	return c.buf[:n], nil
}

func (c *capture) close() { syscall.Close(c.fd) }

// RestartScreen ends the device's screen app (rc_gui_juce). RØDE's
// display-start.sh starts it again within about three seconds, and its new
// connection to the mixer carries a full sync, which reading along needs.
// RØDE's own audio-start.sh does the same whenever the mixer restarts; audio
// is not affected.
func RestartScreen() error {
	pids, err := signalScreen()
	if err != nil {
		return err
	}
	awaitScreen(pids)
	return nil
}

// RestartScreenNow asks the screen app to end and returns at once; the
// fallback (SIGKILL after three seconds) runs in the background. For a
// console that is stopping: the request must leave before it exits.
func RestartScreenNow() error {
	pids, err := signalScreen()
	if err != nil {
		return err
	}
	go awaitScreen(pids)
	return nil
}

func signalScreen() ([]int, error) {
	pids := pidsNamed("rc_gui_juce")
	if len(pids) == 0 {
		return nil, errors.New("the device's screen app (rc_gui_juce) is not running")
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	return pids, nil
}

// awaitScreen waits up to three seconds for the screen app to end, then
// kills it.
func awaitScreen(pids []int) {
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		alive := false
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				alive = true
			}
		}
		if !alive {
			return
		}
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// pidsNamed lists the processes whose command name is name.
func pidsNamed(name string) []int {
	var out []int
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		if b, err := os.ReadFile("/proc/" + d.Name() + "/comm"); err == nil && strings.TrimSpace(string(b)) == name {
			out = append(out, pid)
		}
	}
	return out
}
