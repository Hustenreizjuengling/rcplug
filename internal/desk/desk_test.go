package desk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

func quiet(string, ...interface{}) {}

func startSim(t *testing.T, opts rcsim.Options) *rcsim.Server {
	t.Helper()
	opts.Logf = quiet
	s, err := rcsim.NewDuo(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func run(t *testing.T, cfg Config) (*Client, *Mirror) {
	t.Helper()
	cfg.Logf = quiet
	if cfg.BootGrace == 0 {
		cfg.BootGrace = time.Nanosecond
	}
	m := NewMirror()
	c := NewClient(cfg, m)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c, m
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func live(c *Client) func() bool { return func() bool { return c.Status().State == StateLive } }

func TestMirrorFollowsDevice(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	c, m := run(t, Config{Addr: sim.Addr()})
	waitFor(t, "live", live(c))
	var path []int
	m.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 1) })
	if err := sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mute mirrored", func() bool {
		muted := false
		m.View(func(s Snapshot) { v, _ := s.Root.At(path).Get("channelOutputMute"); muted = v.Truth() })
		return muted
	})
}

func TestWriteIsAtomicAndChecksEpoch(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	c, m := run(t, Config{Addr: sim.Addr()})
	waitFor(t, "live", live(c))
	var epoch uint64
	var path []int
	m.View(func(s Snapshot) { epoch, path = s.Epoch, s.Layout.Path("CHANNEL", 0) })
	build := func(s Snapshot) (*rcproto.Change, error) {
		return &rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelCueEnable", Value: rcproto.Bool(true)}, nil
	}
	if _, err := c.Write(epoch+1, build); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale epoch accepted: %v", err)
	}
	if _, err := c.Write(epoch, build); err != nil {
		t.Fatal(err)
	}
	cue := false
	m.View(func(s Snapshot) { v, _ := s.Root.At(path).Get("channelCueEnable"); cue = v.Truth() })
	if !cue {
		t.Fatal("own write not applied to the mirror")
	}
	waitFor(t, "desk received write", func() bool { return len(sim.Writes()) == 1 })
	if _, err := (&Client{cfg: Config{ReadOnly: true}}).Write(epoch, build); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only client wrote: %v", err)
	}
}

// fakeDesk serves a full sync followed by the given raw frames, then idles.
func fakeDesk(t *testing.T, frames ...[]byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	root, _ := rcsim.DuoTree()
	full := rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: root}).Encode())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write(full)
				for _, f := range frames {
					time.Sleep(20 * time.Millisecond)
					_, _ = conn.Write(f)
				}
				_, _ = bytes.NewBuffer(nil).ReadFrom(conn)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestDesyncForcesFreshSync(t *testing.T) {
	cases := map[string][]byte{
		"change to missing node": rcproto.Frame(rcproto.EncodePropertyChanged([]int{9999}, "x", rcproto.Int(1))),
		"truncated message":      rcproto.Frame([]byte{0x01, 0x01, 0x02}),
		"lost frame boundary":    append([]byte{0xde, 0xad}, rcproto.Frame(rcproto.EncodePropertyChanged([]int{1}, "duckerDepth", rcproto.Double(0.5)))...),
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := run(t, Config{Addr: fakeDesk(t, frame)})
			waitFor(t, "desync", func() bool { return c.Status().Stats.Desyncs > 0 })
			waitFor(t, "second session", func() bool { return c.Status().Stats.Sessions >= 2 })
		})
	}
	// A message type outside the synchroniser's vocabulary is ignored.
	c, _ := run(t, Config{Addr: fakeDesk(t, rcproto.Frame([]byte{0xad, 0x10}))})
	waitFor(t, "unknown type counted", func() bool { return c.Status().Stats.UnknownTypes == 1 })
	if st := c.Status(); st.State != StateLive || st.Stats.Desyncs != 0 {
		t.Fatalf("unknown type treated as desync: %+v", st)
	}
}

func TestStaleAfterDisconnect(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	c, m := run(t, Config{Addr: sim.Addr()})
	waitFor(t, "live", live(c))
	sim.Close()
	waitFor(t, "stale", func() bool {
		stale := false
		m.View(func(s Snapshot) { stale = s.Stale && s.Root != nil })
		return stale
	})
	if _, err := c.Write(0, nil); !errors.Is(err, ErrOffline) {
		t.Fatalf("write while offline: %v", err)
	}
}

func TestBroadcastResyncsLostSubscriberOnQuietDesk(t *testing.T) {
	m := NewMirror()
	root, _ := rcsim.DuoTree()
	if err := m.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: root}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	m.Broadcast(nil) // initial resync after the full sync
	<-s.C
	for i := 0; i < cap(s.C)+1; i++ { // overflow the subscriber
		_ = m.Ingest(&rcproto.Change{Type: rcproto.PropertyChanged, Path: []int{1}, Name: "duckerDepth", Value: rcproto.Double(float64(i))})
		m.Broadcast(nil)
	}
	for len(s.C) > 0 {
		<-s.C
	}
	m.Broadcast(nil) // nothing pending, but the subscriber lost a batch
	select {
	case msg := <-s.C:
		if msg.Event != "resync" {
			t.Fatalf("got %s, want resync", msg.Event)
		}
	default:
		t.Fatal("lost subscriber not resynced on a quiet desk")
	}
}

func TestSecretsRedactedInChangeStream(t *testing.T) {
	m := NewMirror()
	root, _ := rcsim.DuoTree()
	_ = m.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: root})
	s, _ := m.Subscribe()
	m.Broadcast(nil)
	<-s.C
	_ = m.Ingest(&rcproto.Change{Type: rcproto.PropertyChanged, Path: []int{1}, Name: "duckerDepth", Value: rcproto.Double(0.123456789)})
	m.Broadcast(func(typ, prop string) bool { return prop == "duckerDepth" })
	msg := <-s.C
	if strings.Contains(string(msg.Data), "0.123") || !strings.Contains(string(msg.Data), `"r"`) {
		t.Fatalf("double secret leaked: %s", msg.Data)
	}
}

func TestSubscriberLimit(t *testing.T) {
	m := NewMirror()
	for i := 0; i < MaxSubscribers; i++ {
		if _, err := m.Subscribe(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Subscribe(); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit not enforced: %v", err)
	}
}

// With an exclusive (single-client) desk, joining kicks the existing client.
// The guard must notice and stop instead of fighting the GUI.
func TestGuardDetectsDisplacedClient(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc/net/tcp")
	}
	sim := startSim(t, rcsim.Options{Exclusive: true})
	gui, err := net.Dial("tcp", sim.Addr()) // stand-in for the device GUI
	if err != nil {
		t.Fatal(err)
	}
	defer gui.Close()
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(gui) }()
	waitFor(t, "gui connected", func() bool { return sim.Clients() == 1 })
	c, _ := run(t, Config{Addr: sim.Addr(), Guard: true, GuardSettle: 200 * time.Millisecond})
	if c.guardMode != "active" {
		t.Skipf("guard mode %s", c.guardMode)
	}
	waitFor(t, "conflict", func() bool { return c.Status().State == StateConflict })
	if c.Status().Code != "guard_conflict" {
		t.Fatalf("code %q", c.Status().Code)
	}
	sessions := c.Status().Stats.Sessions
	time.Sleep(1500 * time.Millisecond)
	if got := c.Status().Stats.Sessions; got != sessions {
		t.Fatalf("client reconnected after conflict (%d -> %d)", sessions, got)
	}
}

// A Reconnect issued before the conflict must not be taken as consent to
// displace the GUI again.
func TestStaleKickDoesNotOverrideConflict(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc/net/tcp")
	}
	sim := startSim(t, rcsim.Options{Exclusive: true})
	gui, _ := net.Dial("tcp", sim.Addr())
	defer gui.Close()
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(gui) }()
	waitFor(t, "gui connected", func() bool { return sim.Clients() == 1 })
	m := NewMirror()
	c := NewClient(Config{Addr: sim.Addr(), Guard: true, GuardSettle: 300 * time.Millisecond, BootGrace: time.Nanosecond, Logf: quiet}, m)
	c.Reconnect() // no connection yet: the kick token stays queued
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, "conflict", func() bool { return c.Status().State == StateConflict })
	time.Sleep(800 * time.Millisecond)
	if st := c.Status().State; st != StateConflict {
		t.Fatalf("stale kick ended the conflict hold: %s", st)
	}
}

func TestGuardQuietWithMultiClientDesk(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	gui, err := net.Dial("tcp", sim.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer gui.Close()
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(gui) }()
	c, _ := run(t, Config{Addr: sim.Addr(), Guard: true, GuardSettle: 200 * time.Millisecond})
	time.Sleep(1200 * time.Millisecond)
	if st := c.Status(); st.State != StateLive {
		t.Fatalf("state %s (%s)", st.State, st.Error)
	}
}

// A client that leaves by itself while the console joins a multi-client desk
// was not displaced: the guard must not step back.
func TestGuardIgnoresClientLeavingByItself(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	other, err := net.Dial("tcp", sim.Addr())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(other) }()
	time.Sleep(100 * time.Millisecond) // the guard's first look sees it
	c, _ := run(t, Config{Addr: sim.Addr(), Guard: true, GuardSettle: 500 * time.Millisecond})
	waitFor(t, "live", live(c))
	other.Close() // it leaves on its own before the guard looks again
	time.Sleep(1200 * time.Millisecond)
	if st := c.Status(); st.State != StateLive {
		t.Fatalf("state %s (%s)", st.State, st.Error)
	}
}

func TestReconnectAfterDeskRestart(t *testing.T) {
	sim := startSim(t, rcsim.Options{})
	addr := sim.Addr()
	c, m := run(t, Config{Addr: addr})
	waitFor(t, "live", live(c))
	var e1 uint64
	m.View(func(s Snapshot) { e1 = s.Epoch })
	sim.Close()
	waitFor(t, "backoff", func() bool { return c.Status().State == StateBackoff })
	sim2, _ := rcsim.NewDuo(rcsim.Options{Logf: quiet})
	if err := sim2.Listen(addr); err != nil {
		t.Skipf("port reuse: %v", err)
	}
	defer sim2.Close()
	c.Reconnect()
	waitFor(t, "live again", live(c))
	var e2 uint64
	stale := true
	m.View(func(s Snapshot) { e2, stale = s.Epoch, s.Stale })
	if e2 == e1 || stale {
		t.Fatalf("epoch %d->%d stale=%v", e1, e2, stale)
	}
}

func TestHandshakeFallback(t *testing.T) {
	sim := startSim(t, rcsim.Options{WaitHandshake: true})
	c, _ := run(t, Config{Addr: sim.Addr(), SyncTimeout: 2 * time.Second})
	waitFor(t, "live", live(c))
}

// A desk that sends other messages (meters) before it gets the handshake is
// not silent, but it still needs the handshake for its full sync.
func TestHandshakeFallbackWithTrafficBeforeSync(t *testing.T) {
	sim := startSim(t, rcsim.Options{WaitHandshake: true, MeterHz: 15, Animate: true})
	c, _ := run(t, Config{Addr: sim.Addr(), SyncTimeout: 2 * time.Second})
	waitFor(t, "live", live(c))
}

func TestProcNetParsing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tcp")
	content := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:0929 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 100 1\n" +
		"   1: 0100007F:0929 0100007F:A1B2 01 00000000:00000000 00:00000000 00000000     0        0 101 1\n" +
		"   2: 0100007F:A1B2 0100007F:0929 01 00000000:00000000 00:00000000 00000000     0        0 102 1\n" +
		"   3: 3200A8C0:0016 0A00A8C0:C350 01 00000000:00000000 00:00000000 00000000     0        0 103 1\n"
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	old := procNetFiles
	procNetFiles = []string{f}
	defer func() { procNetFiles = old }()
	peers, err := serverPeers(2345)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || !peers["127.0.0.1:"+strconv.Itoa(0xA1B2)] {
		t.Fatalf("peers %v", peers)
	}
	if got := listener(2345); got != "127.0.0.1:2345" {
		t.Fatalf("listener %q", got)
	}
	ip, port, ok := splitHexAddr("0000000000000000FFFF00000100007F:0929")
	if !ok || ip != "127.0.0.1" || port != 2345 {
		t.Fatalf("v4-mapped: %s %d %v", ip, port, ok)
	}
}

// As seen on a Duo with firmware 1.7.6: the GUI holds the only client slot,
// so nothing listens on 2345 and a further client is refused.
func TestBusyHolderNamesTheClientHoldingTheDesk(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tcp")
	held := "  sl  local_address rem_address   st\n" +
		"  10: 0100007F:0929 0100007F:B204 01 00000000:00000000 00:00000000 00000000     0        0 98765432108 1\n" +
		"  11: 0100007F:B204 0100007F:0929 01 00000000:00000000 00:00000000 00000000     0        0 98765432109 1\n" +
		"   6: 0100007F:0929 0100007F:B20C 08 00000000:00000001 00:00000000 00000000     0        0 98765432107 1\n"
	_ = os.WriteFile(f, []byte(held), 0644)
	old := procNetFiles
	procNetFiles = []string{f}
	defer func() { procNetFiles = old }()
	// Socket inodes are 32-bit, so no process owns that one: no process name.
	if got := busyHolder(2345); got != "127.0.0.1:45572" {
		t.Fatalf("busyHolder = %q", got)
	}
	_ = os.WriteFile(f, []byte(held+"   0: 0100007F:0929 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 100 1\n"), 0644)
	if got := busyHolder(2345); got != "" {
		t.Fatalf("with a listener: busyHolder = %q", got)
	}
	_ = os.WriteFile(f, []byte("  sl  local_address rem_address   st\n"), 0644)
	if got := busyHolder(2345); got != "" {
		t.Fatalf("nobody connected: busyHolder = %q", got)
	}
}

// Like rc_audio_mixer: a server that closes its listener once its one client
// is connected. The console must say who holds the desk instead of "not
// reachable".
func TestRefusedBecauseTheOnlySlotIsTaken(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	holder, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	served, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	ln.Close() // the slot is taken: no listener any more
	port := ln.Addr().(*net.TCPAddr).Port
	want := fmt.Sprintf("(pid %d)", os.Getpid())
	if got := busyHolder(port); !strings.Contains(got, want) {
		t.Fatalf("busyHolder = %q, want the test process %s", got, want)
	}
	c, _ := run(t, Config{Addr: ln.Addr().String()})
	waitFor(t, "desk_busy", func() bool { return c.Status().Code == "desk_busy" })
	if st := c.Status(); !strings.Contains(st.Error, "serves one client at a time") || !strings.Contains(st.Error, want) || st.NextRetry != 0 {
		t.Fatalf("status %+v", st)
	}

	// The slot frees up (the holder reconnects in a moment): the console must
	// not take it by itself, only after an explicit reconnect.
	holder.Close()
	served.Close()
	again, err := net.Listen("tcp", ln.Addr().String())
	if err != nil {
		t.Skipf("cannot listen on %s again: %v", ln.Addr(), err)
	}
	defer again.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := again.Accept(); err == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		conn.Close()
		t.Fatal("the console retried by itself and took the free slot")
	case <-time.After(2500 * time.Millisecond): // longer than the first backoff steps
	}
	c.Reconnect()
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("no connection after an explicit reconnect")
	}
}

func TestListenerSpecificAddress(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tcp")
	content := "  sl  local_address rem_address   st\n" +
		"   0: 3200A8C0:0929 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 100 1\n"
	_ = os.WriteFile(f, []byte(content), 0644)
	old := procNetFiles
	procNetFiles = []string{f}
	defer func() { procNetFiles = old }()
	if got := listener(2345); got != "192.168.0.50:2345" {
		t.Fatalf("listener %q", got)
	}
	if got := listener(80); got != "" {
		t.Fatalf("listener(80) %q", got)
	}
}

// A single-client desk that drops us again because the GUI reconnects at
// once must still end in a conflict hold, not in a reconnect loop that keeps
// displacing the GUI.
func TestGuardCatchesFastReconnectingGUI(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc/net/tcp")
	}
	sim := startSim(t, rcsim.Options{Exclusive: true})
	var displaced atomic.Int32
	stopGUI := make(chan struct{})
	defer close(stopGUI)
	go func() { // the GUI: reconnects 100 ms after losing its connection
		for {
			conn, err := net.Dial("tcp", sim.Addr())
			if err == nil {
				_, _ = bytes.NewBuffer(nil).ReadFrom(conn)
				conn.Close()
				displaced.Add(1)
			}
			select {
			case <-stopGUI:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	waitFor(t, "gui connected", func() bool { return sim.Clients() == 1 })
	c, _ := run(t, Config{Addr: sim.Addr(), Guard: true, GuardSettle: 2 * time.Second})
	if c.guardMode != "active" {
		t.Skipf("guard mode %s", c.guardMode)
	}
	waitFor(t, "conflict", func() bool { return c.Status().State == StateConflict })
	n := displaced.Load()
	time.Sleep(1500 * time.Millisecond)
	if got := displaced.Load(); got != n {
		t.Fatalf("GUI displaced again after the conflict (%d -> %d)", n, got)
	}
	if n > 1 {
		t.Errorf("GUI displaced %d times before the guard stepped back", n)
	}
}

// Which side closed a connection decides whether a vanished client was
// displaced: the end that closed first lingers in FIN_WAIT or TIME_WAIT,
// the other one in CLOSE_WAIT.
func TestClosedBy(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tcp")
	content := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		// client 127.0.0.1:41394 (A1B2) left by itself: its end is in TIME_WAIT
		"   0: 0100007F:A1B2 0100007F:0929 06 00000000:00000000 03:00000F9A 00000000     0        0 0 3\n" +
		// client 127.0.0.1:41395 (A1B3) was dropped by the server: the server's end is in TIME_WAIT
		"   1: 0100007F:0929 0100007F:A1B3 06 00000000:00000000 03:00000F9A 00000000     0        0 0 3\n" +
		// client 127.0.0.1:41396 (A1B4): the server closed, the client has not yet (CLOSE_WAIT)
		"   2: 0100007F:A1B4 0100007F:0929 08 00000000:00000000 00:00000000 00000000     0        0 104 1\n"
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	old := procNetFiles
	procNetFiles = []string{f}
	defer func() { procNetFiles = old }()
	for client, want := range map[string]string{
		"127.0.0.1:41394": "client", "127.0.0.1:41395": "server", "127.0.0.1:41396": "server", "127.0.0.1:41397": "",
	} {
		if got := closedBy(2345, client); got != want {
			t.Errorf("closedBy(%s) = %q, want %q", client, got, want)
		}
	}
}
