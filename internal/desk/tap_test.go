package desk

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

// ipTCP builds an IPv4 packet with a TCP segment (checksums are not checked).
func ipTCP(src string, sp int, dst string, dp int, seq uint32, flags byte, payload []byte) []byte {
	b := make([]byte, 40+len(payload))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	b[6] = 0x40 // don't fragment
	b[8], b[9] = 64, 6
	copy(b[12:16], net.ParseIP(src).To4())
	copy(b[16:20], net.ParseIP(dst).To4())
	binary.BigEndian.PutUint16(b[20:], uint16(sp))
	binary.BigEndian.PutUint16(b[22:], uint16(dp))
	binary.BigEndian.PutUint32(b[24:], seq)
	b[32] = 5 << 4
	b[33] = flags | 0x10 // ACK
	if flags&tcpSYN != 0 && dp == 2345 {
		b[33] = flags // the client's SYN carries no ACK
	}
	copy(b[40:], payload)
	return b
}

// conn drives one fake connection between a client port and the mixer.
type fakeConn struct {
	t          *tapper
	port       int
	cseq, dseq uint32
}

func (f *fakeConn) open() {
	f.t.packet(ipTCP("127.0.0.1", f.port, "127.0.0.1", 2345, f.cseq, tcpSYN, nil))
	f.cseq++
	f.t.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", f.port, f.dseq, tcpSYN, nil))
	f.dseq++
}

// segMax is the most one segment carries here (lo allows about 64 KiB).
const segMax = 30000

func (f *fakeConn) desk(p []byte) {
	for len(p) > 0 {
		n := min(len(p), segMax)
		f.t.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", f.port, f.dseq, 0, p[:n]))
		f.dseq += uint32(n)
		p = p[n:]
	}
}

func (f *fakeConn) client(p []byte) {
	for len(p) > 0 {
		n := min(len(p), segMax)
		f.t.packet(ipTCP("127.0.0.1", f.port, "127.0.0.1", 2345, f.cseq, 0, p[:n]))
		f.cseq += uint32(n)
		p = p[n:]
	}
}

func (f *fakeConn) close() {
	f.t.packet(ipTCP("127.0.0.1", f.port, "127.0.0.1", 2345, f.cseq, tcpFIN, nil))
}

func duoFullSync(t *testing.T) ([]byte, *rcproto.Node) {
	t.Helper()
	root, err := rcsim.DuoTree()
	if err != nil {
		t.Fatal(err)
	}
	return rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: root}).Encode()), root
}

func newTestTapper() (*tapper, *Mirror, *[]tapEvent) {
	m := NewMirror()
	var events []tapEvent
	tp := newTapper(2345, m, &Stats{}, func(e tapEvent) { events = append(events, e) })
	return tp, m, &events
}

func muteOf(m *Mirror, path []int) bool {
	muted := false
	m.View(func(s Snapshot) { v, _ := s.Root.At(path).Get("channelOutputMute"); muted = v.Truth() })
	return muted
}

func setMute(path []int, on bool) []byte {
	return rcproto.Frame((&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(on)}).Encode())
}

// The screen connects; the full sync comes in pieces, one twice and two out of
// order; then changes from both sides; then the screen goes away.
func TestTapFollowsTheScreensConnection(t *testing.T) {
	tp, m, events := newTestTapper()
	f := &fakeConn{t: tp, port: 45572, cseq: 1000, dseq: 900000}
	f.open()
	full, _ := duoFullSync(t)
	a, b := full[:7000], full[7000:20000]
	start := f.dseq
	tp.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", f.port, start, 0, a))
	f.dseq = start + uint32(len(a)+len(b))
	f.desk(full[20000:])                                                                 // ahead of a gap
	tp.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", f.port, start, 0, a))                // retransmitted
	tp.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", f.port, start+uint32(len(a)), 0, b)) // fills the gap
	if len(*events) != 1 || (*events)[0].kind != "live" || !m.Ready() {
		t.Fatalf("after the full sync: events %+v, ready %v", *events, m.Ready())
	}
	var path []int
	m.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 1) })

	f.desk(setMute(path, true)) // the mixer reports a change
	if !muteOf(m, path) {
		t.Fatal("a change from the mixer was not mirrored")
	}
	f.client(setMute(path, false)) // the screen changes something; the mixer does not echo it
	if muteOf(m, path) {
		t.Fatal("a change from the screen was not mirrored")
	}
	f.client(full) // a full sync from the client side is ignored
	if muteOf(m, path) {
		t.Fatal("the client's full sync replaced the mixer's tree")
	}
	half := setMute(path, true)
	f.desk(half[:5]) // a frame split across segments
	f.desk(half[5:])
	if !muteOf(m, path) {
		t.Fatal("a split frame was not mirrored")
	}

	f.close()
	if last := (*events)[len(*events)-1]; last.kind != "ended" {
		t.Fatalf("after the screen left: %+v", *events)
	}
	stale := false
	m.View(func(s Snapshot) { stale = s.Stale })
	if !stale {
		t.Fatal("the mirror is not marked stale after the connection ended")
	}

	// The screen restarts: a new connection with a new full sync.
	g := &fakeConn{t: tp, port: 45600, cseq: 5000, dseq: 70000}
	g.open()
	g.desk(full)
	if last := (*events)[len(*events)-1]; last.kind != "live" || muteOf(m, path) {
		t.Fatalf("after the screen came back: %+v, mute %v", *events, muteOf(m, path))
	}
	if tp.stats.FullSyncs != 2 {
		t.Fatalf("full syncs %d", tp.stats.FullSyncs)
	}
}

// Started while the screen is already connected: no full sync, so nothing to
// show until the screen reconnects.
func TestTapJoiningLateWaitsForAFullSync(t *testing.T) {
	tp, m, events := newTestTapper()
	f := &fakeConn{t: tp, port: 45572, cseq: 1000, dseq: 900000}
	f.desk(setMute([]int{1}, true))
	f.client(setMute([]int{1}, false))
	if len(*events) != 0 || m.Ready() {
		t.Fatalf("late join: events %+v, ready %v", *events, m.Ready())
	}
}

// A packet that never comes: the mirror is marked stale instead of silently
// freezing.
func TestTapNoticesALostPacket(t *testing.T) {
	tp, m, events := newTestTapper()
	clock := time.Unix(1000, 0)
	tp.now = func() time.Time { return clock }
	f := &fakeConn{t: tp, port: 45572, cseq: 1000, dseq: 900000}
	f.open()
	full, _ := duoFullSync(t)
	f.desk(full)
	var path []int
	m.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 1) })
	lost := setMute(path, true)
	f.dseq += uint32(len(lost)) // this segment is never seen
	f.desk(setMute(path, false))
	clock = clock.Add(3 * time.Second)
	f.desk(setMute(path, true))
	if last := (*events)[len(*events)-1]; last.kind != "lost" {
		t.Fatalf("events %+v", *events)
	}
	if tp.stats.Desyncs != 1 {
		t.Fatalf("desyncs %d", tp.stats.Desyncs)
	}
}

// Other traffic on the port (a refused probe) does not disturb the mirror.
func TestTapIgnoresOtherClientsAndPorts(t *testing.T) {
	tp, m, events := newTestTapper()
	f := &fakeConn{t: tp, port: 45572, cseq: 1000, dseq: 900000}
	f.open()
	full, _ := duoFullSync(t)
	f.desk(full)
	tp.packet(ipTCP("127.0.0.1", 50000, "127.0.0.1", 2345, 1, tcpSYN, nil))
	tp.packet(ipTCP("127.0.0.1", 2345, "127.0.0.1", 50000, 0, tcpRST, nil))
	tp.packet(ipTCP("127.0.0.1", 9100, "127.0.0.1", 50001, 7, 0, []byte("not ours")))
	if len(*events) != 1 || !m.Ready() {
		t.Fatalf("events %+v", *events)
	}
	stale := true
	m.View(func(s Snapshot) { stale = s.Stale })
	if stale {
		t.Fatal("a refused probe marked the mirror stale")
	}
}

// The real thing on lo (needs root): a client playing the device's screen
// connects to the simulated mixer; the tap follows its connection without
// ever connecting itself. Run with: make test-root
func TestTapReadsAlongOnLoopback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("reading along needs root (make test-root)")
	}
	sim := startSim(t, rcsim.Options{})
	tapMirror := NewMirror()
	tap := NewClient(Config{Addr: sim.Addr(), Mode: "tap", Logf: quiet}, tapMirror)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tap.Run(ctx)
	waitFor(t, "capture open", func() bool { return tap.Status().Code == "tap_waiting" })
	if !tap.Passive() || tap.Status().Mode != "tap" {
		t.Fatalf("not passive: %+v", tap.Status())
	}

	screen, screenMirror := run(t, Config{Addr: sim.Addr()}) // plays the device's screen
	waitFor(t, "screen live", live(screen))
	waitFor(t, "tap live", live(tap))
	if n := sim.Clients(); n != 1 {
		t.Fatalf("the tap connected to the mixer: %d clients", n)
	}

	var path []int
	tapMirror.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 1) })
	if err := sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the mixer's change read along", func() bool { return muteOf(tapMirror, path) })

	var epoch uint64
	screenMirror.View(func(s Snapshot) { epoch = s.Epoch })
	if _, err := screen.Write(epoch, func(s Snapshot) (*rcproto.Change, error) {
		return &rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(false)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the screen's change read along", func() bool { return !muteOf(tapMirror, path) })

	if _, err := tap.Write(epoch, func(s Snapshot) (*rcproto.Change, error) { return nil, nil }); err != ErrPassive {
		t.Fatalf("a passive client wrote: %v", err)
	}
	screen.Reconnect() // the screen reconnects: a new full sync
	waitFor(t, "tap live again", func() bool { return tap.Status().Stats.FullSyncs >= 2 && tap.Status().State == StateLive })
}

// The console restarts (an update) while the screen stays connected: the
// next one goes on with the kept state. Needs root (make test-root).
func TestTapGoesOnAfterARestartOnLoopback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("reading along needs root (make test-root)")
	}
	sim := startSim(t, rcsim.Options{})
	keep := filepath.Join(t.TempDir(), "tap-resume")
	first := NewClient(Config{Addr: sim.Addr(), Mode: "tap", ResumeFile: keep, Logf: quiet}, NewMirror())
	ctx1, stop1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { first.Run(ctx1); close(done1) }()
	waitFor(t, "capture open", func() bool { return first.Status().Code == "tap_waiting" })
	screen, _ := run(t, Config{Addr: sim.Addr()})
	waitFor(t, "screen live", live(screen))
	waitFor(t, "first live", live(first))
	stop1()
	<-done1
	if _, err := os.Stat(keep + ".frame"); err != nil {
		t.Fatalf("no state kept: %v", err)
	}

	mirror := NewMirror()
	second := NewClient(Config{Addr: sim.Addr(), Mode: "tap", ResumeFile: keep, Logf: quiet}, mirror)
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	go second.Run(ctx2)
	waitFor(t, "second capture open", func() bool { return second.Status().Code == "tap_waiting" })
	// The simulator stays silent until something changes (the Duo pings every
	// few seconds); the change is the first traffic the second one sees.
	_, root := duoFullSync(t)
	probe := NewMirror()
	_ = probe.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: root})
	var path []int
	probe.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 2) })
	if err := sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second goes on", func() bool { st := second.Status(); return st.State == StateLive && st.Code == "resumed" })
	waitFor(t, "a change after going on", func() bool { return muteOf(mirror, path) })
}

func TestPortIsAlwaysAValidPort(t *testing.T) {
	for addr, want := range map[string]int{"127.0.0.1:2345": 2345, "127.0.0.1:99999": 2345, "127.0.0.1:-1": 2345, "127.0.0.1:0": 2345, "nonsense": 2345, "[::1]:65535": 65535} {
		if got := NewClient(Config{Addr: addr, Logf: quiet}, NewMirror()).Port(); got != want {
			t.Errorf("Port(%q) = %d, want %d", addr, got, want)
		}
	}
}

// A kept state is used once, only in the same boot and only while fresh.
func TestKeptStateOnlyOnceAndOnlyFromThisBoot(t *testing.T) {
	dir := t.TempDir()
	old := bootIDFile
	bootIDFile = filepath.Join(dir, "boot_id")
	defer func() { bootIDFile = old }()
	_ = os.WriteFile(bootIDFile, []byte("boot-a\n"), 0o644)
	_, root := duoFullSync(t)
	path := filepath.Join(dir, "tap-resume")
	now := time.Unix(5000, 0)

	if err := saveResume(path, "127.0.0.1:45572", root, now); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path + ".frame"); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("kept state not root-only: %v", st)
	}
	r, err := loadResume(path, now.Add(time.Minute))
	if err != nil || r.Client != "127.0.0.1:45572" || r.tree == nil {
		t.Fatalf("load: %+v %v", r, err)
	}
	if _, err := loadResume(path, now.Add(time.Minute)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("used twice: %v", err)
	}

	_ = saveResume(path, "127.0.0.1:45572", root, now)
	_ = os.WriteFile(bootIDFile, []byte("boot-b\n"), 0o644)
	if _, err := loadResume(path, now.Add(time.Minute)); err == nil {
		t.Fatal("used a state from another boot")
	}
	_ = os.WriteFile(bootIDFile, []byte("boot-a\n"), 0o644)
	_ = saveResume(path, "127.0.0.1:45572", root, now)
	if _, err := loadResume(path, now.Add(11*time.Minute)); err == nil {
		t.Fatal("used a stale state")
	}
}

// After a restart the tapper goes on with the kept state as soon as it sees
// the screen's connection again, even if the first bytes are the middle of a
// message; another connection does not get it.
func TestTapGoesOnWithAKeptState(t *testing.T) {
	tp, m, events := newTestTapper()
	_, root := duoFullSync(t)
	tp.resume = &resumeState{Client: "127.0.0.1:45572", tree: root}
	other := &fakeConn{t: tp, port: 45999, cseq: 1, dseq: 1}
	other.desk(setMute([]int{1}, true))
	if len(*events) != 0 || m.Ready() {
		t.Fatalf("another connection used the kept state: %+v", *events)
	}

	f := &fakeConn{t: tp, port: 45572, cseq: 1000, dseq: 900000}
	var path []int
	probe := NewMirror()
	_ = probe.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: root})
	probe.View(func(s Snapshot) { path = s.Layout.Path("CHANNEL", 1) })
	msg := setMute(path, true)
	f.desk(msg[8:]) // the body of a message whose header went by before the restart
	if len(*events) != 1 || (*events)[0].kind != "resumed" || !m.Ready() {
		t.Fatalf("not resumed: %+v", *events)
	}
	f.desk(msg) // a whole message
	if !muteOf(m, path) {
		t.Fatal("a change after resuming was not mirrored")
	}
	if tp.stats.Desyncs != 0 {
		t.Fatalf("lost track while aligning: %d", tp.stats.Desyncs)
	}
}
