package desk

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The GUI on the device's touchscreen is itself a client of rc_audio_mixer's
// control port. Before trusting that the mixer accepts an extra client, the
// connector compares the set of established connections on the port before
// and after it joins: if a connection that existed before vanishes, joining
// displaced someone (most likely the GUI), so the connector backs off for good.

// Peer is one established TCP connection involving the control port.
type Peer struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
	Inode  uint64 `json:"inode"`
	PID    int    `json:"pid,omitempty"`
	Comm   string `json:"comm,omitempty"`
	// Server is true for the mixer's end of a connection (local port = port).
	Server bool `json:"server"`
}

// procNetFiles may be overridden in tests.
var procNetFiles = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// connections lists established TCP sockets whose local or remote port is port.
func connections(port int) ([]Peer, error) {
	var out []Peer
	var firstErr error
	read := 0
	for _, f := range procNetFiles {
		peers, err := parseProcNet(f, port)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		read++
		out = append(out, peers...)
	}
	if read == 0 {
		return nil, firstErr
	}
	return out, nil
}

func parseProcNet(path string, port int) ([]Peer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Peer
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != "01" { // 01 = ESTABLISHED
			continue
		}
		lip, lport, ok1 := splitHexAddr(fields[1])
		rip, rport, ok2 := splitHexAddr(fields[2])
		if !ok1 || !ok2 || (lport != port && rport != port) {
			continue
		}
		inode, _ := strconv.ParseUint(fields[9], 10, 64)
		out = append(out, Peer{
			Local:  fmt.Sprintf("%s:%d", lip, lport),
			Remote: fmt.Sprintf("%s:%d", rip, rport),
			Inode:  inode,
			Server: lport == port,
		})
	}
	return out, sc.Err()
}

// splitHexAddr decodes /proc/net/tcp "0100007F:0929" (IPv4, little-endian
// words) or the 32-hex-digit IPv6 form.
func splitHexAddr(s string) (string, int, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	p, err := strconv.ParseUint(s[i+1:], 16, 16)
	if err != nil {
		return "", 0, false
	}
	h := s[:i]
	switch len(h) {
	case 8:
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return "", 0, false
		}
		return fmt.Sprintf("%d.%d.%d.%d", byte(v), byte(v>>8), byte(v>>16), byte(v>>24)), int(p), true
	case 32:
		// Four little-endian 32-bit words.
		var b [16]byte
		for w := 0; w < 4; w++ {
			v, err := strconv.ParseUint(h[w*8:w*8+8], 16, 32)
			if err != nil {
				return "", 0, false
			}
			b[w*4], b[w*4+1], b[w*4+2], b[w*4+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		}
		// IPv4-mapped (::ffff:a.b.c.d) is shown as plain IPv4.
		mapped := true
		for k := 0; k < 10; k++ {
			if b[k] != 0 {
				mapped = false
			}
		}
		if mapped && b[10] == 0xff && b[11] == 0xff {
			return fmt.Sprintf("%d.%d.%d.%d", b[12], b[13], b[14], b[15]), int(p), true
		}
		var parts []string
		for k := 0; k < 16; k += 2 {
			parts = append(parts, strconv.FormatUint(uint64(b[k])<<8|uint64(b[k+1]), 16))
		}
		return "[" + strings.Join(parts, ":") + "]", int(p), true
	}
	return "", 0, false
}

// listener returns the local address something listens on for port
// ("0.0.0.0" and "::" are mapped to loopback), or "" if none is found.
func listener(port int) string {
	for _, f := range procNetFiles {
		file, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 || fields[3] != "0A" { // 0A = LISTEN
				continue
			}
			ip, p, ok := splitHexAddr(fields[1])
			if !ok || p != port {
				continue
			}
			file.Close()
			if ip == "0.0.0.0" || ip == "[0:0:0:0:0:0:0:0]" {
				return fmt.Sprintf("127.0.0.1:%d", port) // wildcard: loopback works
			}
			return fmt.Sprintf("%s:%d", ip, port)
		}
		file.Close()
	}
	return ""
}

// serverPeers returns the remote endpoints of all clients connected to the
// local server on port (the mixer's ends of the connections).
func serverPeers(port int) (map[string]bool, error) {
	conns, err := connections(port)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range conns {
		if c.Server {
			out[c.Remote] = true
		}
	}
	return out, nil
}

// ClientsOf lists the clients of the local server on port, resolving which
// local process owns each client socket (best effort, needs root for other
// users' processes). Remote clients have no PID.
func ClientsOf(port int) ([]Peer, error) {
	conns, err := connections(port)
	if err != nil {
		return nil, err
	}
	owners := socketOwners()
	byLocal := map[string]Peer{}
	var clients []Peer
	for _, c := range conns {
		if !c.Server {
			byLocal[c.Local] = c
		}
	}
	for _, c := range conns {
		if !c.Server {
			continue
		}
		p := Peer{Local: c.Local, Remote: c.Remote, Inode: c.Inode, Server: true}
		if cl, ok := byLocal[c.Remote]; ok {
			if o, ok := owners[cl.Inode]; ok {
				p.PID, p.Comm = o.pid, o.comm
			}
		}
		clients = append(clients, p)
	}
	return clients, nil
}

type owner struct {
	pid  int
	comm string
}

// socketOwners maps socket inodes to the owning process by scanning /proc.
func socketOwners() map[uint64]owner {
	out := map[uint64]owner{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(d, "fd"))
		if err != nil {
			continue
		}
		var comm string
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(d, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			ino, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
			if err != nil {
				continue
			}
			if comm == "" {
				b, _ := os.ReadFile(filepath.Join(d, "comm"))
				comm = strings.TrimSpace(string(b))
			}
			out[ino] = owner{pid: pid, comm: comm}
		}
	}
	return out
}

// uptimeSeconds returns the system uptime, or -1 if unknown.
func uptimeSeconds() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return -1
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return -1
	}
	return v
}
