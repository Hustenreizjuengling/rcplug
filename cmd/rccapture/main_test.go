package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

func TestCaptureReadsAndSanitizes(t *testing.T) {
	sim, err := rcsim.NewDuo(rcsim.Options{Logf: func(string, ...interface{}) {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer sim.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tree, err := capture(ctx, sim.Addr(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := sim.Snapshot()
	if !bytes.Equal(tree.AppendTo(nil), want.AppendTo(nil)) {
		t.Fatal("captured tree differs from the desk's")
	}
	if len(sim.Writes()) != 0 {
		t.Fatalf("capture wrote to the desk: %v", sim.Writes())
	}
	san := rcproto.Sanitize(tree)
	if v, _ := findProp(san, "SYSTEM", "systemName"); v.S != rcproto.Redacted {
		t.Errorf("systemName = %q after sanitizing", v.S)
	}
	if v, _ := findProp(san, "SYSTEM", "systemFirmwareVersion"); v.S == rcproto.Redacted || v.S == "" {
		t.Errorf("firmware version lost: %q", v.S)
	}
}

func TestCaptureTimesOutWithoutDesk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := capture(ctx, "127.0.0.1:1", false); err == nil {
		t.Fatal("expected an error without a desk")
	}
}

// On a desk that serves one client, the capture pushes the device's screen
// off; with the guard on, that is reported.
func TestCaptureReportsDisplacedClient(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("the guard needs /proc/net/tcp")
	}
	sim, err := rcsim.NewDuo(rcsim.Options{Exclusive: true, Logf: func(string, ...interface{}) {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	gui, err := net.Dial("tcp", sim.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer gui.Close()
	go func() { _, _ = io.Copy(io.Discard, gui) }()
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tree, err := capture(ctx, sim.Addr(), true)
	if !errors.Is(err, errDisplaced) || tree == nil {
		t.Fatalf("capture = %v, %v; want the tree and errDisplaced", tree != nil, err)
	}
}
