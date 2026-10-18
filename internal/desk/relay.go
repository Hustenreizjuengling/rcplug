package desk

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Full control (experimental): the mixer serves exactly one client, normally
// the device's screen, and reading along can only watch it. In relay mode the
// console takes that one place itself and then serves the screen on the
// mixer's port in turn (the mixer stops listening while it has its client).
// Every message passes through unchanged in both directions; a change the
// console makes goes to the mixer and to the screen, which shows it. So every
// setting can be changed from the browser, not only those with a button.
//
// What keeps the device safe:
//   - The mixer listens again as soon as its client leaves. The console's
//     listener therefore always closes first (also when the console
//     crashes: a duplicate of the mixer connection above every other
//     descriptor keeps it open until the end, see keepHigh), so the mixer
//     never finds its port taken.
//   - When the session ends (switched off, the console stops, the mixer
//     went away), the screen app is restarted, as "Read the state again"
//     does, and connects to the mixer afresh. If the console crashes, the
//     screen just loses its connection.
//   - Nothing waits on the screen: frames for it are queued, and a screen
//     that does not keep up is disconnected (it reconnects and gets the
//     state anew). The mixer connection is read without pause.
//   - Without a screen for a few seconds the console restarts the screen app
//     once: one that tried to connect in the moment between the mixer
//     closing its listener and the console opening its own does not try
//     again (seen on a real Duo after an update of the console). Without a
//     screen for longer the console steps back, so the screen finds the
//     mixer, and tries to take over again a little later (a few times).

// Relay timing; tests shorten them.
var (
	relayDial       = 200 * time.Millisecond // connect timeout to the mixer
	relayBind       = 2 * time.Second        // for the mixer to stop listening
	relayPoll       = 20 * time.Millisecond  // retry interval while the place is free soon
	relayTakeover   = 6 * time.Second        // after restarting the screen: how long to try
	relayBoot       = 2 * time.Minute        // at start: how long to wait for the mixer
	relayNudge      = 8 * time.Second        // without a screen: restart it once
	relayNoScreen   = 30 * time.Second       // step back after so long without a screen
	relayBootNudge  = 45 * time.Second       // the same while the device boots (its screen
	relayBootAlone  = 2 * time.Minute        // app takes a while to come up)
	relayRetry      = 2 * time.Minute        // after stepping back: when to take over again
	relayRetries    = 3                      // and how often
	relayPing       = time.Second            // keep-alive to the mixer while no screen is there
	relayScreenSend = 2 * time.Second        // write deadline towards the screen
	relayQueue      = 8192                   // frames queued for the screen
)

// restartScreen restarts the device's screen app (its init script starts it
// again within about three seconds); tests replace it.
var restartScreen = RestartScreen

// booting reports whether the device started less than relayBoot ago, when
// its screen app is still coming up; tests replace it (a CI machine may
// have just started too).
var booting = func() bool {
	up := uptimeSeconds()
	return up >= 0 && up < relayBoot.Seconds()
}

var (
	errRelayBusy    = sessionErr("relay_busy", "the device's screen holds the mixer's only place; restart it to take over")
	errRelayRace    = sessionErr("relay_race", "the device's screen connected to the mixer again first")
	errRelayNoMixer = sessionErr("relay_no_mixer", "the mixer does not accept connections")
	errNoScreen     = sessionErr("relay_no_screen", "the device's screen did not connect through the console; stepping back so it finds the mixer")
)

// relay is one session of relay mode.
type relay struct {
	c    *Client
	up   net.Conn
	keep int // high duplicate of up's descriptor, -1 = none
	ln   net.Listener

	upMu sync.Mutex // whole frames to the mixer

	mu     sync.Mutex // guards tree, screen and the times; never held while writing to a socket
	tree   *rcproto.Node
	screen *screenConn
	alone  time.Time // since when no screen is connected
	since  time.Time // since when the current screen is connected

	synced chan struct{} // closed with the mixer's first full sync
	fail   chan error    // the first fatal error ends the session
	once   sync.Once
}

type screenConn struct {
	conn net.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func (s *screenConn) close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

// RelayStatus describes relay mode for the UI.
type RelayStatus struct {
	Screen       bool   `json:"screen"` // the device's screen is connected through the console
	ScreenSince  string `json:"screenSince,omitempty"`
	ScreenFrames uint64 `json:"screenFrames"` // frames from the screen to the mixer
}

// acquire connects to the mixer as its one client. With takeover it
// restarts the device's screen first if the screen holds the place; wait is
// how long to wait for a mixer that is not up yet.
func (c *Client) acquire(ctx context.Context, takeover bool, wait time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(wait)
	restarted := false
	d := net.Dialer{Timeout: relayDial}
	for {
		conn, err := d.DialContext(ctx, "tcp", c.cfg.Addr)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		held := busyHolder(c.Port()) != ""
		switch {
		case held && restarted:
			return nil, errRelayRace
		case held && takeover:
			c.cfg.Logf("desk: full control: restarting the device's screen to take its place at the mixer")
			if err := restartScreen(); err != nil {
				return nil, sessionErr("relay_failed", "restarting the device's screen: %v", err)
			}
			restarted, takeover = true, false
			deadline = time.Now().Add(relayTakeover)
		case held:
			return nil, errRelayBusy
		}
		if time.Now().After(deadline) {
			if restarted {
				return nil, errRelayRace
			}
			return nil, errRelayNoMixer
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(relayPoll):
		}
	}
}

// runRelay holds the mixer's place and serves the screen until ctx ends or
// the session fails. The listener is always closed before the mixer
// connection.
func (c *Client) runRelay(ctx context.Context, takeover bool, wait time.Duration) error {
	c.setState(StateConnecting, "relay_connecting", "taking the mixer's place for full control")
	up, err := c.acquire(ctx, takeover, wait)
	if err != nil {
		return err
	}
	if tc, ok := up.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(1 << 20)
		_ = tc.SetNoDelay(true)
	}
	r := &relay{c: c, up: up, keep: -1, synced: make(chan struct{}), fail: make(chan error, 1), alone: time.Now()}
	if r.keep, err = keepHigh(up); err != nil {
		_ = up.Close()
		return sessionErr("relay_failed", "keeping the mixer connection safe: %v", err)
	}
	// The mixer stops listening once it has taken the connection, which
	// can be a moment after the connection is up.
	for deadline := time.Now().Add(relayBind); ; {
		r.ln, err = net.Listen("tcp", c.cfg.Addr)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) || ctx.Err() != nil {
			// The mixer keeps listening (another firmware?) or someone else
			// has the port: give the place back at once.
			r.closeUpstream()
			return sessionErr("relay_failed", "serving the screen on %s: %v", c.cfg.Addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	c.relay = r
	c.conn = up
	c.stats.Sessions++
	c.mu.Unlock()
	c.cfg.Logf("desk: full control: connected to the mixer, serving the device's screen on %s", c.cfg.Addr)
	defer func() {
		r.close()
		c.mu.Lock()
		c.relay, c.conn = nil, nil
		c.mu.Unlock()
		c.mirror.SetStale(true)
		// Whatever the screen app does when its connection ends, a restart
		// (as RØDE's scripts and "Read the state again" do it) connects it to
		// the mixer afresh. Not waited for: the console may be stopping.
		restart := restartScreen
		go func() { _ = restart() }()
		c.cfg.Logf("desk: full control ended; the device's screen restarts and connects to the mixer")
	}()
	c.setState(StateSyncing, "relay_syncing", "")

	go r.fromMixer()
	go r.acceptScreens()
	return r.watch(ctx)
}

// watch ends the session on ctx, a fatal error, or a long time without a
// screen, and keeps the mixer company while no screen is there.
func (r *relay) watch(ctx context.Context) error {
	t := time.NewTicker(relayPing)
	defer t.Stop()
	ping := rcproto.Frame([]byte("ping"))
	timeout := time.NewTimer(r.c.cfg.SyncTimeout)
	defer timeout.Stop()
	nudge, noScreen := relayNudge, relayNoScreen
	if booting() {
		nudge, noScreen = relayBootNudge, relayBootAlone
	}
	var nudged time.Time // the alone period a restart of the screen was for
	// Like the client: if the mixer stays silent, the session-open message
	// asks for the state (TCP normally needs none).
	hello := time.NewTimer(r.c.handshakeAfter())
	defer hello.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-r.fail:
			return err
		case <-hello.C:
			select {
			case <-r.synced:
			default:
				if r.c.cfg.Handshake != "never" {
					r.c.cfg.Logf("desk: full control: no full sync yet, sending the session-open handshake")
					_ = r.toMixer(rcproto.Frame(rcproto.SessionOpen))
				}
			}
		case <-timeout.C:
			select {
			case <-r.synced:
			default:
				return sessionErr("no_full_sync", "the mixer sent no state")
			}
		case <-t.C:
			r.mu.Lock()
			alone, since := r.screen == nil, r.alone
			r.mu.Unlock()
			if !alone {
				continue
			}
			if time.Since(since) > noScreen {
				return errNoScreen
			}
			if time.Since(since) > nudge && !nudged.Equal(since) {
				nudged = since
				r.c.cfg.Logf("desk: full control: the device's screen has not connected; restarting it so it connects through the console")
				restart := restartScreen
				go func() { _ = restart() }()
			}
			if err := r.toMixer(ping); err != nil {
				return err
			}
		}
	}
}

func (r *relay) failWith(err error) {
	select {
	case r.fail <- err:
	default:
	}
}

// toMixer writes one whole frame to the mixer.
func (r *relay) toMixer(frame []byte) error {
	r.upMu.Lock()
	defer r.upMu.Unlock()
	_ = r.up.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := r.up.Write(frame); err != nil {
		r.failWith(sessionErr("disconnected", "writing to the mixer: %v", err))
		return err
	}
	return nil
}

// fromMixer forwards the mixer's frames to the screen and applies them.
func (r *relay) fromMixer() {
	c := r.c
	fr := rcproto.NewFrameReader(r.up)
	synced := false
	for {
		body, err := fr.Next()
		if err != nil {
			if !synced {
				r.failWith(sessionErr("no_full_sync", "no complete device state received: %v", err))
			} else {
				r.failWith(sessionErr("disconnected", "the mixer closed the connection: %v", err))
			}
			return
		}
		c.mu.Lock()
		c.stats.FramesIn++
		c.stats.BytesIn += uint64(len(body)) + 8
		c.mu.Unlock()
		frame := rcproto.Frame(body)
		ch, mine := decodeTwice(body)
		r.mu.Lock()
		if ch != nil {
			if root, err := rcproto.Apply(r.tree, ch); err == nil {
				r.tree = root
			}
		}
		if r.screen != nil {
			r.queueLocked(frame)
		}
		r.mu.Unlock()
		if mine == nil {
			continue
		}
		if !synced && mine.Type != rcproto.FullSync {
			continue
		}
		if err := c.mirror.Ingest(mine); err != nil {
			c.mu.Lock()
			c.stats.ApplyErrors++
			c.mu.Unlock()
			continue
		}
		if mine.Type == rcproto.FullSync {
			c.mu.Lock()
			c.stats.FullSyncs++
			c.mu.Unlock()
			if !synced {
				synced = true
				close(r.synced)
				nodes, props := 0, 0
				c.mirror.View(func(s Snapshot) { nodes, props = s.Root.Count() })
				c.cfg.Logf("desk: full control: live, full sync %d bytes, %d nodes, %d properties", len(body), nodes, props)
				r.setStatus()
			}
		}
	}
}

// decodeTwice decodes a ValueTreeSynchroniser message into two independent
// changes, one for the relay's own tree and one for the mirror (applying a
// change hands its subtree to the tree). nil for anything else (pings).
func decodeTwice(body []byte) (*rcproto.Change, *rcproto.Change) {
	if len(body) == 0 || body[0] < byte(rcproto.PropertyChanged) || body[0] > byte(rcproto.PropertyRemoved) {
		return nil, nil
	}
	a, err := rcproto.DecodeChange(body)
	if err != nil {
		return nil, nil
	}
	b, _ := rcproto.DecodeChange(body)
	return a, b
}

// queueLocked hands a frame to the screen's writer; a screen that does not
// keep up is disconnected rather than ever holding up the mixer.
func (r *relay) queueLocked(frame []byte) {
	s := r.screen
	select {
	case s.out <- frame:
	default:
		r.c.cfg.Logf("desk: full control: the device's screen does not keep up; disconnecting it (it reconnects)")
		r.dropScreenLocked(s)
	}
}

func (r *relay) dropScreenLocked(s *screenConn) {
	s.close()
	if r.screen == s {
		r.screen = nil
		r.alone = time.Now()
	}
}

// acceptScreens serves the screen: one at a time, a new one replaces the old.
func (r *relay) acceptScreens() {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go r.attach(conn)
	}
}

// attach gives a newly connected screen the current state, then forwards.
func (r *relay) attach(conn net.Conn) {
	select {
	case <-r.synced:
	case <-time.After(r.c.cfg.SyncTimeout):
		_ = conn.Close()
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	s := &screenConn{conn: conn, out: make(chan []byte, relayQueue), done: make(chan struct{})}
	r.mu.Lock()
	if r.screen != nil {
		r.c.cfg.Logf("desk: full control: a new connection from the device's screen replaces the old one")
		r.dropScreenLocked(r.screen)
	}
	s.out <- rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: r.tree}).Encode())
	r.screen, r.since = s, time.Now()
	r.mu.Unlock()
	r.c.cfg.Logf("desk: full control: the device's screen is connected through the console")
	r.setStatus()
	go r.toScreen(s)
	r.fromScreen(s)
}

func (r *relay) toScreen(s *screenConn) {
	for {
		select {
		case <-s.done:
			return
		case b := <-s.out:
			_ = s.conn.SetWriteDeadline(time.Now().Add(relayScreenSend))
			if _, err := s.conn.Write(b); err != nil {
				r.mu.Lock()
				r.dropScreenLocked(s)
				r.mu.Unlock()
				return
			}
		}
	}
}

// fromScreen forwards the screen's frames to the mixer unchanged and applies
// its changes (the mixer does not echo them). A full sync from the screen is
// its own idea of the tree and only passes through.
func (r *relay) fromScreen(s *screenConn) {
	c := r.c
	defer func() {
		r.mu.Lock()
		r.dropScreenLocked(s)
		r.mu.Unlock()
		r.setStatus()
	}()
	fr := rcproto.NewFrameReader(s.conn)
	for {
		body, err := fr.Next()
		if err != nil {
			return
		}
		if r.toMixer(rcproto.Frame(body)) != nil {
			return
		}
		c.mu.Lock()
		c.relayFrames++
		c.mu.Unlock()
		ch, mine := decodeTwice(body)
		if ch == nil || ch.Type == rcproto.FullSync {
			continue
		}
		r.mu.Lock()
		if root, err := rcproto.Apply(r.tree, ch); err == nil {
			r.tree = root
		}
		r.mu.Unlock()
		if err := c.mirror.Ingest(mine); err != nil {
			c.mu.Lock()
			c.stats.ApplyErrors++
			c.mu.Unlock()
		}
	}
}

// send delivers a change of the console to the mixer and to the screen.
func (r *relay) send(ch *rcproto.Change) error {
	if ch.Type != rcproto.PropertyChanged {
		return sdk.NewError(http.StatusBadRequest, "invalid_change", "only property changes can be written")
	}
	body := ch.Encode()
	frame := rcproto.Frame(body)
	if err := r.toMixer(frame); err != nil {
		return sdk.NewError(http.StatusServiceUnavailable, "send_failed", "sending to the desk failed: "+err.Error())
	}
	own, _ := decodeTwice(body)
	r.mu.Lock()
	if own != nil {
		if root, err := rcproto.Apply(r.tree, own); err == nil {
			r.tree = root
		}
	}
	if r.screen != nil {
		r.queueLocked(frame)
	}
	r.mu.Unlock()
	r.c.mu.Lock()
	r.c.stats.FramesOut++
	r.c.mu.Unlock()
	return nil
}

func (r *relay) setStatus() {
	select {
	case <-r.synced:
	default:
		return
	}
	r.mu.Lock()
	screen := r.screen != nil
	r.mu.Unlock()
	if screen {
		r.c.setState(StateLive, "", "")
	} else {
		r.c.setState(StateLive, "relay_no_screen", "full control; the device's screen is not connected (it reconnects within seconds)")
	}
}

// status for the UI.
func (r *relay) status() *RelayStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := &RelayStatus{Screen: r.screen != nil}
	if st.Screen {
		st.ScreenSince = r.since.Format(time.RFC3339)
	}
	return st
}

// close ends the session: the listener first, then the screen, then the
// mixer connection, so the mixer can always listen again.
func (r *relay) close() {
	r.once.Do(func() {
		_ = r.ln.Close()
		r.mu.Lock()
		if r.screen != nil {
			r.dropScreenLocked(r.screen)
		}
		r.mu.Unlock()
		r.closeUpstream()
	})
}

func (r *relay) closeUpstream() {
	closeKeep(r.keep)
	r.keep = -1
	_ = r.up.Close()
}

// SetFullControl switches relay mode on or off (reading along only). Switched
// on, the console takes the mixer's place: at once if it is free, else by
// restarting the device's screen (about three seconds). Off, it gives the
// place back and reads along again.
func (c *Client) SetFullControl(on bool) {
	c.mu.Lock()
	if c.mode != "tap" {
		c.mu.Unlock()
		return
	}
	trigger := c.wantRelay && !on // give the place back
	c.wantRelay = on
	if on && c.relay == nil {
		c.takeover, trigger = true, true
		c.relayCode, c.relayErr = "", ""
	}
	sw := c.switchMode
	c.mu.Unlock()
	if trigger && sw != nil {
		sw()
	}
}

// relayWanted reports whether relay mode is wanted and whether an explicit
// takeover request is pending (and consumes it).
func (c *Client) relayWanted() (want, takeover bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	want, takeover = c.wantRelay, c.takeover
	c.takeover = false
	return
}

// placeFree: the mixer listens and nobody is connected.
var placeFree = func(port int) bool { return listener(port) != "" && busyHolder(port) == "" }

// runDevice reads along with the device's screen, or relays while full
// control is wanted. Takeovers (restarting the screen) happen only at the
// start and on an explicit request; otherwise the console relays when it
// finds the mixer's place free.
func (c *Client) runDevice(ctx context.Context) {
	defer c.setState(StateStopped, "", "")
	first := true
	retries := 0          // takeovers tried again after stepping back
	var retryAt time.Time // when to try the next one (zero: none)
	for ctx.Err() == nil {
		want, takeover := c.relayWanted()
		if want {
			wait := relayDial
			if first {
				takeover = true
				if up := uptimeSeconds(); up >= 0 && up < relayBoot.Seconds() {
					wait = relayBoot - time.Duration(up*float64(time.Second))
				}
			}
			sctx, cancel := context.WithCancel(ctx)
			c.mu.Lock()
			c.switchMode = cancel
			c.relayRetry = time.Time{}
			c.mu.Unlock()
			err := c.runRelay(sctx, takeover, wait)
			cancel()
			if ctx.Err() != nil {
				return
			}
			code, msg := "relay_ended", "full control ended"
			var se *sessionError
			if errors.As(err, &se) {
				code, msg = se.code, se.Error()
			} else if err != nil && !errors.Is(err, context.Canceled) {
				msg = err.Error()
			}
			if errors.Is(err, context.Canceled) {
				code, msg = "", ""
			}
			c.mu.Lock()
			c.relayCode, c.relayErr = code, msg
			c.mu.Unlock()
			if msg != "" {
				c.cfg.Logf("desk: full control: %s", msg)
			}
			retryAt = time.Time{}
			switch {
			case (code == "relay_no_screen" || code == "relay_race") && retries < relayRetries:
				retries++
				retryAt = time.Now().Add(relayRetry)
				c.cfg.Logf("desk: full control: trying again at %s (%d of %d)", retryAt.Format("15:04:05"), retries, relayRetries)
			case code != "relay_no_screen" && code != "relay_race":
				retries = 0
			}
			c.mu.Lock()
			c.relayRetry = retryAt
			c.mu.Unlock()
		}
		first = false
		tctx, cancel := context.WithCancel(ctx)
		c.mu.Lock()
		c.switchMode = cancel
		want = c.wantRelay
		c.mu.Unlock()
		watched := make(chan struct{})
		if want {
			at := retryAt
			go func() { c.watchPlace(tctx, cancel, at); close(watched) }()
		} else {
			close(watched)
		}
		c.runTap(tctx)
		cancel()
		<-watched
	}
}

// watchPlace ends reading along when the mixer's place becomes free while
// full control is wanted (the screen is gone, the mixer restarted), or at
// retryAt (not zero) to take the place over again.
func (c *Client) watchPlace(ctx context.Context, switchMode context.CancelFunc, retryAt time.Time) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var retry <-chan time.Time
	if !retryAt.IsZero() {
		timer := time.NewTimer(time.Until(retryAt))
		defer timer.Stop()
		retry = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry:
			c.mu.Lock()
			c.takeover = true
			c.mu.Unlock()
			switchMode()
			return
		case <-t.C:
			if placeFree(c.Port()) {
				switchMode()
				return
			}
		}
	}
}
