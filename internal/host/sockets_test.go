package host

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// A connection between two local processes names both, as needed to see who
// holds a port such as the mixer's.
func TestSocketTableNamesBothEndsOfALocalConnection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	raw, _ := os.ReadFile("/proc/self/comm")
	me := fmt.Sprintf("%s (%d)", strings.TrimSpace(string(raw)), os.Getpid())
	out := socketTable()
	listen, conns, _ := strings.Cut(out, "TCP connections")
	if !strings.Contains(listen, ln.Addr().String()+" ") || !strings.Contains(listen, me) {
		t.Errorf("listener %s of %s missing:\n%s", ln.Addr(), me, listen)
	}
	want := fmt.Sprintf("%s <-> %s", me, me)
	found := false
	for _, line := range strings.Split(conns, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ln.Addr().String()) && strings.Contains(line, "ESTABLISHED") && strings.Contains(line, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("connection on %s with both owners (%s) missing:\n%s", ln.Addr(), want, conns)
	}
}

func TestProcAddrDecodesTheKernelsFormats(t *testing.T) {
	for in, want := range map[string]string{
		"0100007F:0929":                         "127.0.0.1:2345",
		"3200A8C0:13C4":                         "192.168.0.50:5060",
		"00000000000000000000000000000000:1F90": "[::]:8080",
		"0000000000000000FFFF00000100007F:0929": "127.0.0.1:2345", // IPv4-mapped
		"00000000000000000000000001000000:0016": "[::1]:22",
	} {
		if got, ok := procAddr(in); !ok || got != want {
			t.Errorf("procAddr(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := procAddr("zz:0929"); ok {
		t.Error("bad address accepted")
	}
}

func TestShortAddrCutsPublicAddressesOnly(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:2345":           "127.0.0.1:2345",
		"192.168.0.50:5060":        "192.168.0.50:5060",
		"10.0.0.2:22":              "10.0.0.2:22",
		"0.0.0.0:22":               "0.0.0.0:22",
		"[::]:8080":                "[::]:8080",
		"[fe80::1]:5353":           "[fe80::1]:5353",
		"34.242.56.56:47032":       "34.242.x.x:47032",
		"[2a02:810d:4b3f::17]:443": "[2a02:810d:…]:443",
		"not an address":           "not an address",
	} {
		if got := shortAddr(in); got != want {
			t.Errorf("shortAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every name on the bus is listed with its process; the mixer on the Duo is
// there only as :1.N.
func TestDbusTableNamesTheProcesses(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "dbus-send")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*ListNames*) printf 'method return time=1.0 sender=org.freedesktop.DBus\n   array [\n      string "org.freedesktop.DBus"\n      string ":1.7"\n   ]\n' ;;
*"string::1.7"*) echo "   uint32 %d" ;;
*) echo "   uint32 1" ;;
esac
`, os.Getpid())
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := dbusSend
	dbusSend = fake
	defer func() { dbusSend = old }()
	out := dbusTable(context.Background())
	raw, _ := os.ReadFile("/proc/self/comm")
	if want := regexp.MustCompile(fmt.Sprintf(`:1\.7 +%s \(%d\)`, regexp.QuoteMeta(strings.TrimSpace(string(raw))), os.Getpid())); !want.MatchString(out) {
		t.Errorf("want %s in:\n%s", want, out)
	}
	if !strings.Contains(out, "org.freedesktop.DBus") {
		t.Errorf("bus daemon missing:\n%s", out)
	}

	dbusSend = filepath.Join(t.TempDir(), "missing")
	if out := dbusTable(context.Background()); out != "" {
		t.Errorf("without dbus-send: %q", out)
	}
}
