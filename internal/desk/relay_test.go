package desk

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

// fakeScreen behaves like rc_gui_juce towards the mixer's port: it connects,
// keeps the tree it receives, pings, and connects again whenever its
// connection ends.
type fakeScreen struct {
	addr string

	mu       sync.Mutex
	conn     net.Conn
	tree     *rcproto.Node
	syncs    int
	pings    int
	paused   time.Time // no reconnect before this (a restart of the app)
	sessions int
	stop     chan struct{}
	wg       sync.WaitGroup
}

func startScreen(t *testing.T, addr string) *fakeScreen { return startScreenAt(t, addr, time.Time{}) }

// startScreenAt starts a screen that does not connect before until (or a
// restart).
func startScreenAt(t *testing.T, addr string, until time.Time) *fakeScreen {
	f := &fakeScreen{addr: addr, stop: make(chan struct{}), paused: until}
	f.wg.Add(1)
	go f.run()
	t.Cleanup(func() {
		close(f.stop)
		f.mu.Lock()
		if f.conn != nil {
			f.conn.Close()
		}
		f.mu.Unlock()
		f.wg.Wait()
	})
	return f
}

func (f *fakeScreen) run() {
	defer f.wg.Done()
	for {
		select {
		case <-f.stop:
			return
		default:
		}
		f.mu.Lock()
		wait := time.Until(f.paused)
		f.mu.Unlock()
		if wait > 0 {
			time.Sleep(min(wait, 20*time.Millisecond)) // a restart shortens the pause
			continue
		}
		conn, err := net.DialTimeout("tcp", f.addr, 100*time.Millisecond)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		f.mu.Lock()
		select {
		case <-f.stop: // the cleanup ran while we dialled: it did not see this connection
			f.mu.Unlock()
			conn.Close()
			return
		default:
		}
		f.conn = conn
		f.sessions++
		f.mu.Unlock()
		done := make(chan struct{})
		go func() { // pings, as the screen sends them
			t := time.NewTicker(50 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					f.mu.Lock()
					_, _ = conn.Write(rcproto.Frame([]byte("ping")))
					f.mu.Unlock()
				}
			}
		}()
		fr := rcproto.NewFrameReader(conn)
		for {
			body, err := fr.Next()
			if err != nil {
				break
			}
			f.mu.Lock()
			if string(body) == "ping" {
				f.pings++
			} else if ch, err := rcproto.DecodeChange(body); err == nil {
				if ch.Type == rcproto.FullSync {
					f.syncs++
				}
				if root, err := rcproto.Apply(f.tree, ch); err == nil {
					f.tree = root
				}
			}
			f.mu.Unlock()
		}
		close(done)
		conn.Close()
		f.mu.Lock()
		f.conn = nil
		f.mu.Unlock()
	}
}

// restart is what restarting rc_gui_juce does to its connection.
func (f *fakeScreen) restart() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = time.Now().Add(300 * time.Millisecond)
	if f.conn != nil {
		f.conn.Close()
	}
	return nil
}

func (f *fakeScreen) send(t *testing.T, ch *rcproto.Change) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn == nil {
		t.Fatal("screen not connected")
	}
	if _, err := f.conn.Write(rcproto.Frame(ch.Encode())); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeScreen) value(path []int, name string) (rcproto.Value, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tree == nil {
		return rcproto.Value{}, false
	}
	n := f.tree.At(path)
	if n == nil {
		return rcproto.Value{}, false
	}
	return n.Get(name)
}

func (f *fakeScreen) connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn != nil && f.tree != nil
}

func oneClientSim(t *testing.T, opts rcsim.Options) *rcsim.Server {
	t.Helper()
	opts.Logf = func(string, ...interface{}) {}
	sim, err := rcsim.NewDuo(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Close)
	return sim
}

// relayClient returns a client reading along (where it cannot: it is not
// root) on addr, and functions to start and stop it.
func relayClient(t *testing.T, addr string) (c *Client, start, stop func()) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	c = NewClient(Config{Addr: addr, Mode: "tap", Logf: t.Logf}, NewMirror())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var once sync.Once
	started := false
	start = func() { started = true; go func() { c.Run(ctx); close(done) }() }
	stop = func() {
		once.Do(func() {
			cancel()
			if started {
				<-done
			}
		})
	}
	t.Cleanup(stop)
	return c, start, stop
}

func fdOf(t *testing.T, sc interface {
	SyscallConn() (syscall.RawConn, error)
}) int {
	t.Helper()
	raw, err := sc.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	_ = raw.Control(func(f uintptr) { fd = int(f) })
	return fd
}

func fastRelay(t *testing.T) {
	old := []time.Duration{relayPing, relayNoScreen, relayTakeover}
	relayPing, relayNoScreen, relayTakeover = 50*time.Millisecond, 5*time.Second, 3*time.Second
	oldBooting := booting
	booting = func() bool { return false } // whatever the test machine's uptime
	t.Cleanup(func() { relayPing, relayNoScreen, relayTakeover = old[0], old[1], old[2]; booting = oldBooting })
}

func TestFullControlFromTheStart(t *testing.T) {
	fastRelay(t)
	sim := oneClientSim(t, rcsim.Options{OneClient: true, Ping: 50 * time.Millisecond})
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "relay live", func() bool { st := c.Status(); return st.Mode == "relay" && st.State == StateLive })
	if sim.Listening() || sim.Clients() != 1 {
		t.Fatalf("the console should hold the mixer's place: listening=%v clients=%d", sim.Listening(), sim.Clients())
	}
	if c.Passive() {
		t.Fatal("passive while relaying")
	}

	// The screen connects to the mixer's port, which the console now serves.
	screen := startScreen(t, sim.Addr())
	waitFor(t, "screen through the relay", func() bool { return screen.connected() && c.Status().Relay.Screen })
	var l *rcproto.Layout
	c.mirror.View(func(s Snapshot) { l = s.Layout })
	ch0 := l.Path("CHANNEL", 0)
	want := sim.Snapshot().At(ch0)
	if v, _ := screen.value(ch0, "channelOutputMute"); !v.Equal(mustGet(want, "channelOutputMute")) {
		t.Fatalf("the screen got another state than the mixer's")
	}
	waitFor(t, "pings forwarded to the screen", func() bool { screen.mu.Lock(); defer screen.mu.Unlock(); return screen.pings > 0 })

	// The screen's changes reach the mixer and the console's mirror.
	mute := !mustGet(want, "channelOutputMute").Truth()
	screen.send(t, &rcproto.Change{Type: rcproto.PropertyChanged, Path: ch0, Name: "channelOutputMute", Value: rcproto.Bool(mute)})
	waitFor(t, "screen change at the mixer", func() bool { return mustGet(sim.Snapshot().At(ch0), "channelOutputMute").Truth() == mute })
	waitFor(t, "screen change in the mirror", func() bool { return mirrorValue(c, ch0, "channelOutputMute").Truth() == mute })

	// The mixer's changes reach the screen and the mirror.
	ch1 := l.Path("CHANNEL", 1)
	cue := !mustGet(sim.Snapshot().At(ch1), "channelCueEnable").Truth()
	if err := sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: ch1, Name: "channelCueEnable", Value: rcproto.Bool(cue)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mixer change at the screen", func() bool { v, _ := screen.value(ch1, "channelCueEnable"); return v.Truth() == cue })
	waitFor(t, "mixer change in the mirror", func() bool { return mirrorValue(c, ch1, "channelCueEnable").Truth() == cue })

	// The console's own write reaches both.
	var epoch uint64
	c.mirror.View(func(s Snapshot) { epoch = s.Epoch })
	if _, err := c.Write(epoch, func(s Snapshot) (*rcproto.Change, error) {
		return &rcproto.Change{Type: rcproto.PropertyChanged, Path: ch1, Name: "channelCueEnable", Value: rcproto.Bool(!cue)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "console write at the mixer", func() bool { return mustGet(sim.Snapshot().At(ch1), "channelCueEnable").Truth() == !cue })
	waitFor(t, "console write at the screen", func() bool { v, _ := screen.value(ch1, "channelCueEnable"); return v.Truth() == !cue })

	// The listener sits below the kept duplicate of the mixer connection.
	c.mu.Lock()
	r := c.relay
	c.mu.Unlock()
	if runtime.GOOS == "linux" {
		lfd := fdOf(t, r.ln.(*net.TCPListener))
		if r.keep < 200 || lfd >= r.keep {
			t.Errorf("descriptor order: listener %d, kept mixer connection %d", lfd, r.keep)
		}
	}

	// A reconnect does not touch full control.
	c.Reconnect()
	time.Sleep(200 * time.Millisecond)
	if st := c.Status(); st.Mode != "relay" || st.State != StateLive || sim.Clients() != 1 {
		t.Fatalf("after a reconnect: %s %s, %d mixer clients", st.Mode, st.State, sim.Clients())
	}

	// Switched off: the place goes back, the screen finds the mixer.
	c.SetFullControl(false)
	waitFor(t, "screen at the mixer again", func() bool {
		return sim.Clients() == 1 && !sim.Listening() && c.Status().Mode == "tap" && screen.connected()
	})
	if n := sim.RelistenFailures(); n != 0 {
		t.Fatalf("the mixer found its port taken %d time(s)", n)
	}
}

func TestFullControlTakesThePlaceOfTheScreen(t *testing.T) {
	fastRelay(t)
	sim := oneClientSim(t, rcsim.Options{OneClient: true})
	screen := startScreen(t, sim.Addr())
	waitFor(t, "screen at the mixer", func() bool { return screen.connected() && !sim.Listening() })
	old := restartScreen
	oldNow := restartScreenNow
	restartScreen, restartScreenNow = screen.restart, screen.restart
	t.Cleanup(func() { restartScreen, restartScreenNow = old, oldNow })

	c, start, stop := relayClient(t, sim.Addr())
	start()
	time.Sleep(100 * time.Millisecond)
	if st := c.Status(); st.Mode != "tap" {
		t.Fatalf("mode %s without full control", st.Mode)
	}
	c.SetFullControl(true)
	waitFor(t, "screen through the relay", func() bool {
		st := c.Status()
		return st.Mode == "relay" && st.Relay != nil && st.Relay.Screen && screen.connected()
	})
	screen.mu.Lock()
	sessions := screen.sessions
	screen.mu.Unlock()
	if sessions != 2 {
		t.Errorf("screen sessions %d, want 2 (mixer, then the console)", sessions)
	}

	// The console stops (an update): the screen goes back to the mixer.
	stop()
	waitFor(t, "screen at the mixer after the console stopped", func() bool { return sim.Clients() == 1 && screen.connected() && !sim.Listening() })
	if n := sim.RelistenFailures(); n != 0 {
		t.Fatalf("the mixer found its port taken %d time(s)", n)
	}
}

func TestFullControlStepsBackWithoutAScreen(t *testing.T) {
	fastRelay(t)
	relayNoScreen = 300 * time.Millisecond
	sim := oneClientSim(t, rcsim.Options{OneClient: true})
	old := placeFree
	placeFree = func(int) bool { return false } // stay reading along afterwards
	t.Cleanup(func() { placeFree = old })
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "relay", func() bool { return c.Status().Mode == "relay" })
	waitFor(t, "stepping back", func() bool { st := c.Status(); return st.Mode == "tap" && st.RelayCode == "relay_no_screen" })
	waitFor(t, "mixer listening again", sim.Listening)
	if n := sim.RelistenFailures(); n != 0 {
		t.Fatalf("relisten failures %d", n)
	}
}

func TestFullControlNeedsTheMixerToStopListening(t *testing.T) {
	fastRelay(t)
	sim := oneClientSim(t, rcsim.Options{}) // keeps listening with a client
	old := placeFree
	placeFree = func(int) bool { return false }
	t.Cleanup(func() { placeFree = old })
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "giving up", func() bool { return c.Status().RelayCode == "relay_failed" })
	waitFor(t, "place given back", func() bool { return sim.Clients() == 0 })
}

func TestFullControlDropsAScreenThatDoesNotRead(t *testing.T) {
	fastRelay(t)
	oldQ := relayQueue
	relayQueue = 4
	t.Cleanup(func() { relayQueue = oldQ })
	sim := oneClientSim(t, rcsim.Options{OneClient: true, MeterHz: 200})
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "relay live", func() bool { st := c.Status(); return st.Mode == "relay" && st.State == StateLive })
	stuck, err := net.Dial("tcp", sim.Addr()) // connects, never reads
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()
	if tc, ok := stuck.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	waitFor(t, "stuck screen dropped", func() bool { st := c.Status(); return st.Relay != nil && !st.Relay.Screen && st.Stats.FramesIn > 2000 })
	in := c.Status().Stats.FramesIn
	waitFor(t, "the mixer is still read", func() bool { return c.Status().Stats.FramesIn > in+500 })
}

func mustGet(n *rcproto.Node, name string) rcproto.Value {
	v, _ := n.Get(name)
	return v
}

func mirrorValue(c *Client, path []int, name string) (v rcproto.Value) {
	c.mirror.View(func(s Snapshot) {
		if n := s.Root.At(path); n != nil {
			v, _ = n.Get(name)
		}
	})
	return
}

func TestFullControlAsksASilentMixerForItsState(t *testing.T) {
	fastRelay(t)
	sim := oneClientSim(t, rcsim.Options{OneClient: true, WaitHandshake: true})
	c, start, _ := relayClient(t, sim.Addr())
	c.cfg.SyncTimeout = 2 * time.Second // handshake after 0.4 s
	c.SetFullControl(true)
	start()
	waitFor(t, "relay live after the handshake", func() bool { st := c.Status(); return st.Mode == "relay" && st.State == StateLive })
}

// A screen that missed the console's listener (it tried in the moment the
// mixer had stopped listening and the console had not started yet) and does
// not try again: the console restarts it once, and it connects through the
// console (seen on a real Duo after an update of the console).
func TestFullControlNudgesAScreenThatMissedIt(t *testing.T) {
	fastRelay(t)
	old := []time.Duration{relayNudge}
	relayNudge = 300 * time.Millisecond
	t.Cleanup(func() { relayNudge = old[0] })
	sim := oneClientSim(t, rcsim.Options{OneClient: true})
	screen := startScreenAt(t, sim.Addr(), time.Now().Add(time.Hour))
	restarts := 0
	var mu sync.Mutex
	oldRestart := restartScreen
	restartScreen = func() error { mu.Lock(); restarts++; mu.Unlock(); return screen.restart() }
	t.Cleanup(func() { restartScreen = oldRestart })

	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "screen through the relay after a nudge", func() bool {
		st := c.Status()
		return st.Mode == "relay" && st.Relay != nil && st.Relay.Screen && screen.connected()
	})
	mu.Lock()
	n := restarts
	mu.Unlock()
	if n != 1 {
		t.Fatalf("screen restarted %d times, want 1", n)
	}
	time.Sleep(500 * time.Millisecond)
	if st := c.Status(); st.Mode != "relay" || st.RelayCode != "" {
		t.Fatalf("after the nudge: %+v", st)
	}
}

// After stepping back for want of a screen, the console takes over again a
// little later, a few times, and then stays reading along.
func TestFullControlTriesAgainAfterSteppingBack(t *testing.T) {
	fastRelay(t)
	old := []time.Duration{relayNoScreen, relayRetry, relayNudge}
	relayNoScreen, relayRetry, relayNudge = 200*time.Millisecond, 200*time.Millisecond, time.Hour
	t.Cleanup(func() { relayNoScreen, relayRetry, relayNudge = old[0], old[1], old[2] })
	oldPlace := placeFree
	placeFree = func(int) bool { return false } // only the retries take over
	t.Cleanup(func() { placeFree = oldPlace })
	oldRestart := restartScreen
	restartScreen = func() error { return nil }
	t.Cleanup(func() { restartScreen = oldRestart })
	sim := oneClientSim(t, rcsim.Options{OneClient: true})
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "first session and three more", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.stats.Sessions == uint64(1+relayRetries)
	})
	waitFor(t, "reading along at the end", func() bool { st := c.Status(); return st.Mode == "tap" && st.RelayCode == "relay_no_screen" })
	time.Sleep(time.Second)
	c.mu.Lock()
	sessions := c.stats.Sessions
	c.mu.Unlock()
	if sessions != uint64(1+relayRetries) {
		t.Fatalf("%d sessions, want %d", sessions, 1+relayRetries)
	}
}

// A copy that no longer follows the mixer (here: a screen change it cannot
// apply) ends the session: the copy goes stale and full control is tried
// again later.
func TestFullControlEndsWhenTheCopyLosesTrack(t *testing.T) {
	fastRelay(t)
	sim := oneClientSim(t, rcsim.Options{OneClient: true, Ping: 50 * time.Millisecond})
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "relay live", func() bool { st := c.Status(); return st.Mode == "relay" && st.State == StateLive })
	screen := startScreen(t, sim.Addr())
	waitFor(t, "screen through the relay", func() bool { return screen.connected() && c.Status().Relay.Screen })
	screen.send(t, &rcproto.Change{Type: rcproto.PropertyChanged, Path: []int{9999}, Name: "x", Value: rcproto.Bool(true)})
	waitFor(t, "session ended as desync", func() bool { st := c.Status(); return st.RelayCode == "relay_desync" && st.RelayRetry != "" })
	stale := false
	c.mirror.View(func(s Snapshot) { stale = s.Stale })
	if !stale && c.Status().Mode == "relay" {
		t.Fatal("the copy that lost track is still trusted")
	}
}

// Another local program connecting to the mixer's port does not cut the
// device's screen off.
func TestFullControlKeepsTheScreenForOtherClients(t *testing.T) {
	fastRelay(t)
	n := 0
	old := peerComm
	peerComm = func(net.Conn) string {
		n++
		if n == 1 {
			return screenApp
		}
		return "rccapture"
	}
	t.Cleanup(func() { peerComm = old })
	sim := oneClientSim(t, rcsim.Options{OneClient: true, Ping: 50 * time.Millisecond})
	c, start, _ := relayClient(t, sim.Addr())
	c.SetFullControl(true)
	start()
	waitFor(t, "relay live", func() bool { st := c.Status(); return st.Mode == "relay" && st.State == StateLive })
	screen := startScreen(t, sim.Addr())
	waitFor(t, "screen through the relay", func() bool { return screen.connected() && c.Status().Relay.Screen })
	other, err := net.Dial("tcp", sim.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_ = other.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := other.Read(make([]byte, 16)); err == nil {
		t.Fatal("the other client was served")
	}
	if !screen.connected() || !c.Status().Relay.Screen {
		t.Fatal("the screen was cut off")
	}
}

// While a plugin has the device's screen (rcplugd's marker), full control
// stays alone with the mixer: no restart of the screen, no stepping back.
// When the marker goes, the waits for RØDE's screen start afresh.
func TestFullControlStaysWhileAPluginHasTheScreen(t *testing.T) {
	fastRelay(t)
	old := []time.Duration{relayNudge, relayNoScreen}
	relayNudge, relayNoScreen = 300*time.Millisecond, 900*time.Millisecond
	t.Cleanup(func() { relayNudge, relayNoScreen = old[0], old[1] })
	restarts := 0
	var mu sync.Mutex
	oldRestart := restartScreen
	restartScreen = func() error { mu.Lock(); restarts++; mu.Unlock(); return nil }
	t.Cleanup(func() { restartScreen = oldRestart })
	oldFree := placeFree
	placeFree = func(int) bool { return false }
	t.Cleanup(func() { placeFree = oldFree })

	marker := filepath.Join(t.TempDir(), "rcplug-screen")
	os.WriteFile(marker, []byte("screendemo\n"), 0o644)
	sim := oneClientSim(t, rcsim.Options{OneClient: true})
	c, start, _ := relayClient(t, sim.Addr())
	c.cfg.ScreenMarker = marker
	c.SetFullControl(true)
	start()
	waitFor(t, "relay", func() bool { return c.Status().Mode == "relay" })
	time.Sleep(2 * time.Second) // well past the nudge and the step back
	mu.Lock()
	n := restarts
	mu.Unlock()
	if st := c.Status(); st.Mode != "relay" || n != 0 || st.Code != "screen_away" || !strings.Contains(st.Error, "screendemo") {
		t.Fatalf("with the screen away: %+v, %d restarts", st, n)
	}

	// The screen comes back: the nudge and the step back come in their own
	// time from now on, not at once.
	os.Remove(marker)
	time.Sleep(150 * time.Millisecond)
	if st := c.Status(); st.Mode != "relay" {
		t.Fatalf("stepped back at once: %+v", st)
	}
	waitFor(t, "the screen nudged", func() bool { mu.Lock(); defer mu.Unlock(); return restarts == 1 })
	waitFor(t, "stepping back without the screen", func() bool { return c.Status().RelayCode == "relay_no_screen" })
}
