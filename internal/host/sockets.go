package host

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// socketTable lists the TCP and UDP sockets of the machine (listening and
// connected) and its named unix sockets, each with the process that owns it:
// what "connection refused" or a taken port needs. For a connection between
// two local processes both are named. Public addresses are cut short.
func socketTable() string {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		return "(no /proc/net)\n"
	}
	owners := socketOwners()
	var b strings.Builder
	tcp := readProcNet("/proc/net/tcp", "/proc/net/tcp6")
	byLocal := map[string]procSocket{}
	for _, s := range tcp {
		byLocal[s.local] = s
	}
	b.WriteString("TCP listening\n")
	for _, s := range tcp {
		if s.state == "0A" {
			fmt.Fprintf(&b, "  %-28s %s\n", shortAddr(s.local), owners.name(s.inode))
		}
	}
	b.WriteString("\nTCP connections (local <-> remote, state, owner <-> owner of the remote end if it is on this machine)\n")
	for _, s := range tcp {
		if s.state == "0A" {
			continue
		}
		peer := ""
		if p, ok := byLocal[s.remote]; ok {
			peer = " <-> " + owners.name(p.inode)
		}
		fmt.Fprintf(&b, "  %-28s <-> %-28s %-11s %s%s\n", shortAddr(s.local), shortAddr(s.remote), tcpState(s.state), owners.name(s.inode), peer)
	}
	b.WriteString("\nUDP\n")
	for _, s := range readProcNet("/proc/net/udp", "/proc/net/udp6") {
		remote := ""
		if !strings.HasSuffix(s.remote, ":0") {
			remote = " <-> " + shortAddr(s.remote)
		}
		fmt.Fprintf(&b, "  %-28s%s %s\n", shortAddr(s.local), remote, owners.name(s.inode))
	}
	b.WriteString("\nUnix sockets with a name (path, owners)\n")
	named, unnamed := unixSockets(owners)
	for _, line := range named {
		b.WriteString("  " + line + "\n")
	}
	if len(unnamed) > 0 {
		fmt.Fprintf(&b, "\nUnnamed unix sockets per process: %s\n", strings.Join(unnamed, ", "))
	}
	return b.String()
}

type procSocket struct {
	local, remote, state string
	inode                uint64
}

// readProcNet reads /proc/net/{tcp,udp}[6], sorted by local port.
func readProcNet(paths ...string) []procSocket {
	var out []procSocket
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			local, ok1 := procAddr(fields[1])
			remote, ok2 := procAddr(fields[2])
			ino, _ := strconv.ParseUint(fields[9], 10, 64)
			if ok1 && ok2 {
				out = append(out, procSocket{local: local, remote: remote, state: fields[3], inode: ino})
			}
		}
		f.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return portOf(out[i].local) < portOf(out[j].local) })
	return out
}

// procAddr decodes "0100007F:0929" (IPv4) or the 32-digit IPv6 form, both
// in the kernel's little-endian words, to "127.0.0.1:2345" / "[::1]:2345".
func procAddr(s string) (string, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 || (i != 8 && i != 32) {
		return "", false
	}
	port, err := strconv.ParseUint(s[i+1:], 16, 16)
	if err != nil {
		return "", false
	}
	ip := make(net.IP, i/2)
	for w := 0; w < i/8; w++ {
		v, err := strconv.ParseUint(s[w*8:w*8+8], 16, 32)
		if err != nil {
			return "", false
		}
		ip[w*4], ip[w*4+1], ip[w*4+2], ip[w*4+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), true
}

func portOf(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return n
}

// shortAddr keeps loopback, private and link-local addresses and cuts a
// public one short (34.242.x.x), so the bundle does not name the servers and
// clients of the user's setup.
func shortAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return addr
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.x.x:%s", v4[0], v4[1], port)
	}
	parts := strings.SplitN(ip.String(), ":", 3)
	return "[" + parts[0] + ":" + parts[1] + ":…]:" + port
}

var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1", "05": "FIN_WAIT2",
	"06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT", "09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

func tcpState(hex string) string {
	if s, ok := tcpStates[hex]; ok {
		return s
	}
	return hex
}

// sockOwners maps socket inodes to the processes holding them.
type sockOwners map[uint64][]string

func (o sockOwners) name(ino uint64) string {
	if ino == 0 {
		return "-" // TIME_WAIT and the like belong to no process any more
	}
	if names, ok := o[ino]; ok {
		return strings.Join(names, " + ")
	}
	return "?"
}

func socketOwners() sockOwners {
	out := sockOwners{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		fds, err := os.ReadDir(filepath.Join(d, "fd"))
		if err != nil {
			continue
		}
		comm := ""
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(d, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			ino, err := strconv.ParseUint(strings.TrimSuffix(link[len("socket:["):], "]"), 10, 64)
			if err != nil {
				continue
			}
			if comm == "" {
				raw, _ := os.ReadFile(filepath.Join(d, "comm"))
				comm = fmt.Sprintf("%s (%s)", strings.TrimSpace(string(raw)), filepath.Base(d))
			}
			if names := out[ino]; len(names) == 0 || names[len(names)-1] != comm {
				out[ino] = append(names, comm)
			}
		}
	}
	return out
}

// unixSockets returns one line per named unix socket ("path  owners", the
// abstract ones with a leading @) and, per process, how many unnamed ones it
// holds.
func unixSockets(owners sockOwners) (named, unnamed []string) {
	f, err := os.Open("/proc/net/unix")
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	byPath := map[string]map[string]bool{}
	count := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 7 {
			continue
		}
		ino, _ := strconv.ParseUint(fields[6], 10, 64)
		who := owners.name(ino)
		if len(fields) < 8 {
			count[who]++
			continue
		}
		if byPath[fields[7]] == nil {
			byPath[fields[7]] = map[string]bool{}
		}
		byPath[fields[7]][who] = true
	}
	for path, who := range byPath {
		var names []string
		for n := range who {
			names = append(names, n)
		}
		sort.Strings(names)
		named = append(named, fmt.Sprintf("%-44s %s", path, strings.Join(names, ", ")))
	}
	sort.Strings(named)
	for who, n := range count {
		unnamed = append(unnamed, fmt.Sprintf("%s %d", who, n))
	}
	sort.Strings(unnamed)
	return named, unnamed
}

// dbusTable lists who is on the D-Bus system bus, with the process behind
// each name. It uses dbus-send, which the device has; "" without it.
func dbusTable(ctx context.Context) string {
	if _, err := lookPath(dbusSend); err != nil {
		return ""
	}
	call := func(method string, args ...string) []byte {
		a := append([]string{"--system", "--print-reply", "--dest=org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus." + method}, args...)
		return commandOutput(ctx, 2*time.Second, dbusSend, a...)
	}
	list := call("ListNames")
	if list == nil {
		return "(the system bus did not answer)\n"
	}
	var b strings.Builder
	b.WriteString("Names on the D-Bus system bus (name, process). A program on the bus without a\n" +
		"well-known name appears only as :1.N. Method calls are allowed only where a policy\n" +
		"in /etc/dbus-1/system.d or /usr/share/dbus-1/system.d permits them.\n\n")
	for _, name := range dbusStrings(list) {
		who := "?"
		for _, s := range strings.Fields(string(call("GetConnectionUnixProcessID", "string:"+name))) {
			if pid, err := strconv.Atoi(s); err == nil && pid > 0 {
				raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
				who = fmt.Sprintf("%s (%d)", strings.TrimSpace(string(raw)), pid)
			}
		}
		fmt.Fprintf(&b, "  %-44s %s\n", name, who)
	}
	return b.String()
}

// dbusSend is the dbus-send binary (tests replace it); lookPath likewise.
var (
	dbusSend = "dbus-send"
	lookPath = exec.LookPath
)

// dbusStrings returns the string values of a dbus-send --print-reply answer.
func dbusStrings(reply []byte) []string {
	var out []string
	for _, line := range strings.Split(string(reply), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `string "`) && strings.HasSuffix(line, `"`) {
			out = append(out, line[len(`string "`):len(line)-1])
		}
	}
	sort.Strings(out)
	return out
}
