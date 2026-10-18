package desk

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Connection states reported to the UI.
const (
	StateWaiting    = "waiting"    // boot grace: letting the device's own GUI connect first
	StateConnecting = "connecting" // dialing
	StateSyncing    = "syncing"    // connected, waiting for the full sync
	StateLive       = "live"       // mirror is current
	StateBackoff    = "backoff"    // disconnected, retrying soon
	StateConflict   = "conflict"   // joining displaced another client; stopped until manual retry
	StateStopped    = "stopped"
)

// Config configures a Client.
type Config struct {
	// Addr of rc_audio_mixer's control port (default 127.0.0.1:2345).
	Addr string
	// Guard protects existing clients (the device GUI); see guard.go. It is
	// only active for local addresses, where /proc/net/tcp sees both ends.
	Guard bool
	// BootGrace delays the first connection until the system has been up this
	// long, so the GUI is connected before we join (default 75 s).
	BootGrace time.Duration
	// SyncTimeout bounds the wait for the initial full sync (default 15 s).
	SyncTimeout time.Duration
	// GuardSettle is how long after the sync the guard re-checks (default 2 s).
	GuardSettle time.Duration
	// ReadOnly hard-disables every write regardless of UI settings.
	ReadOnly bool
	// Handshake controls the USB-style session-open message (AD 10 A7 B0):
	// "auto" sends it once if the desk stays silent after connecting (TCP
	// normally needs none), "always" sends it right away, "never" disables it.
	Handshake string
	Logf      func(format string, args ...interface{})
}

// Stats are cumulative counters for diagnostics.
type Stats struct {
	FramesIn     uint64 `json:"framesIn"`
	BytesIn      uint64 `json:"bytesIn"`
	FramesOut    uint64 `json:"framesOut"`
	DecodeErrors uint64 `json:"decodeErrors"`
	UnknownTypes uint64 `json:"unknownTypes"`
	ApplyErrors  uint64 `json:"applyErrors"`
	Desyncs      uint64 `json:"desyncs"`
	Sessions     uint64 `json:"sessions"`
	FullSyncs    uint64 `json:"fullSyncs"`
}

// Status is the connection state snapshot served to the UI.
type Status struct {
	State       string  `json:"state"`
	Addr        string  `json:"addr"`
	Code        string  `json:"code,omitempty"`  // machine-readable reason for Error
	Error       string  `json:"error,omitempty"` // English detail
	Since       string  `json:"since"`
	LiveSeconds float64 `json:"liveSeconds"`
	NextRetry   float64 `json:"nextRetrySeconds,omitempty"`
	Wait        float64 `json:"waitSeconds,omitempty"` // boot grace remaining
	Guard       string  `json:"guard"`                 // off | active | unavailable | remote
	ReadOnly    bool    `json:"readOnly"`
	Rate        float64 `json:"changesPerSecond"`
	Stats       Stats   `json:"stats"`
}

// sessionError is how a session ended (code for the UI, detail for humans).
type sessionError struct {
	code string
	err  error
}

func (e *sessionError) Error() string { return e.err.Error() }
func (e *sessionError) Unwrap() error { return e.err }

func sessionErr(code string, format string, args ...interface{}) error {
	return &sessionError{code: code, err: fmt.Errorf(format, args...)}
}

var errConflict = &sessionError{code: "guard_conflict", err: errors.New(
	"another client of the desk (probably the device GUI) lost its connection when we joined; " +
		"staying disconnected until a manual reconnect")}

// Client maintains the connection to rc_audio_mixer and feeds the Mirror.
type Client struct {
	cfg    Config
	mirror *Mirror

	mu        sync.Mutex
	conn      net.Conn
	state     string
	code      string
	lastErr   string
	since     time.Time
	liveAt    time.Time
	retryAt   time.Time
	waitUntil time.Time
	guardMode string
	stats     Stats
	onStatus  func(Status)

	wmu  sync.Mutex // serialises writes on the socket
	kick chan struct{}
}

// NewClient creates a client feeding mirror.
func NewClient(cfg Config, mirror *Mirror) *Client {
	if cfg.Addr == "" {
		cfg.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(rcproto.DefaultPort))
	}
	if cfg.BootGrace == 0 {
		cfg.BootGrace = 75 * time.Second
	}
	if cfg.SyncTimeout == 0 {
		cfg.SyncTimeout = 15 * time.Second
	}
	if cfg.GuardSettle == 0 {
		cfg.GuardSettle = 2 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	c := &Client{cfg: cfg, mirror: mirror, state: StateStopped, since: time.Now(), kick: make(chan struct{}, 1)}
	c.guardMode = c.computeGuardMode()
	return c
}

// ReadOnly reports whether writes are hard-disabled.
func (c *Client) ReadOnly() bool { return c.cfg.ReadOnly }

// Addr is the configured desk address.
func (c *Client) Addr() string { return c.cfg.Addr }

// Mirror returns the mirror this client feeds.
func (c *Client) Mirror() *Mirror { return c.mirror }

// OnStatus registers a callback invoked on every state change.
func (c *Client) OnStatus(fn func(Status)) {
	c.mu.Lock()
	c.onStatus = fn
	c.mu.Unlock()
}

func (c *Client) computeGuardMode() string {
	if !c.cfg.Guard {
		return "off"
	}
	if !isLocalAddr(c.cfg.Addr) {
		return "remote"
	}
	if _, err := connections(c.Port()); err != nil {
		return "unavailable"
	}
	return "active"
}

// isLocalAddr reports whether addr points at this machine (loopback or one of
// its interface addresses), where /proc/net/tcp sees both ends of a connection.
func isLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// Port is the TCP port of the configured desk address.
func (c *Client) Port() int {
	_, p, err := net.SplitHostPort(c.cfg.Addr)
	if err != nil {
		return rcproto.DefaultPort
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return rcproto.DefaultPort
	}
	return n
}

func (c *Client) setState(s, code, errText string) {
	c.mu.Lock()
	changed := c.state != s || c.lastErr != errText || c.code != code
	c.state, c.code, c.lastErr = s, code, errText
	if changed {
		c.since = time.Now()
	}
	if s == StateLive {
		c.liveAt = time.Now()
	}
	fn := c.onStatus
	c.mu.Unlock()
	if changed && fn != nil {
		fn(c.Status())
	}
}

// Status returns the current connection status.
func (c *Client) Status() Status {
	rate := c.mirror.Rate()
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{
		State: c.state, Addr: c.cfg.Addr, Code: c.code, Error: c.lastErr,
		Since: c.since.Format(time.RFC3339), Guard: c.guardMode,
		ReadOnly: c.cfg.ReadOnly, Rate: rate, Stats: c.stats,
	}
	switch c.state {
	case StateLive:
		st.LiveSeconds = time.Since(c.liveAt).Seconds()
	case StateBackoff:
		if d := time.Until(c.retryAt).Seconds(); d > 0 {
			st.NextRetry = d
		}
	case StateWaiting:
		if d := time.Until(c.waitUntil).Seconds(); d > 0 {
			st.Wait = d
		}
	}
	return st
}

// Reconnect drops the current session and connects again (fresh full sync).
// After a conflict it is the explicit user override that allows a new try.
func (c *Client) Reconnect() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run maintains the connection until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	defer c.setState(StateStopped, "", "")
	backoff := time.Second
	for ctx.Err() == nil {
		if c.guardMode == "active" {
			if up := uptimeSeconds(); up >= 0 && up < c.cfg.BootGrace.Seconds() {
				wait := time.Duration((c.cfg.BootGrace.Seconds() - up) * float64(time.Second))
				c.mu.Lock()
				c.waitUntil = time.Now().Add(wait)
				c.mu.Unlock()
				c.setState(StateWaiting, "boot_grace", "device is still starting")
				if !c.sleep(ctx, wait) {
					return
				}
			}
		}
		began := time.Now()
		err := c.session(ctx)
		c.mirror.SetStale(true)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errConflict) {
			c.setState(StateConflict, errConflict.code, errConflict.Error())
			c.cfg.Logf("desk: %v", err)
			// A token left by an earlier Reconnect must not count as consent.
			select {
			case <-c.kick:
			default:
			}
			select { // stay away until the user explicitly retries
			case <-ctx.Done():
				return
			case <-c.kick:
				backoff = time.Second
				continue
			}
		}
		if time.Since(began) > time.Minute {
			backoff = time.Second
		}
		code, msg := "disconnected", "connection closed"
		var se *sessionError
		if errors.As(err, &se) {
			code, msg = se.code, se.Error()
		} else if err != nil {
			msg = err.Error()
		}
		if code == "desk_busy" {
			// The mixer serves one client at a time and someone (normally the
			// device's screen) holds it. A retry could take the screen's place
			// the moment it reconnects, so only an explicit reconnect tries again.
			c.mu.Lock()
			c.retryAt = time.Time{}
			c.mu.Unlock()
			c.setState(StateBackoff, code, msg)
			c.cfg.Logf("desk: %s; not retrying until a manual reconnect", msg)
			select {
			case <-c.kick:
			default:
			}
			select {
			case <-ctx.Done():
				return
			case <-c.kick:
				backoff = time.Second
				continue
			}
		}
		c.mu.Lock()
		c.retryAt = time.Now().Add(backoff)
		c.mu.Unlock()
		c.setState(StateBackoff, code, msg)
		c.cfg.Logf("desk: %s, retry in %s", msg, backoff)
		if !c.sleepOrKick(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (c *Client) sleepOrKick(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	case <-c.kick:
	}
	return true
}

// session runs one connection: dial, full sync, guard check, read loop.
//
// The read loop never waits on anything but the socket and a short mirror
// lock: a client that stops reading could make the desk block on its sends.
// Once live, anything that could make the mirror diverge from the desk (a
// garbled message, a change that does not apply, a lost frame boundary) ends
// the session, so the next connection starts from a fresh full sync instead
// of letting index paths — and therefore writes — point at the wrong node.
func (c *Client) session(ctx context.Context) error {
	var before map[string]bool
	if c.guardMode == "active" {
		var err error
		if before, err = serverPeers(c.Port()); err != nil {
			c.cfg.Logf("desk: guard pre-check failed: %v", err)
			before = nil
		}
	}
	c.setState(StateConnecting, "", "")
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	addr := c.cfg.Addr
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil && errors.Is(err, syscall.ECONNREFUSED) && isLocalAddr(addr) {
		// The mixer may listen on a specific interface address only; ask the
		// kernel where the port is actually bound and try that.
		if alt := listener(c.Port()); alt != "" && alt != addr {
			c.cfg.Logf("desk: %s refused, desk listens on %s, using that", addr, alt)
			addr = alt
			conn, err = d.DialContext(ctx, "tcp", addr)
		}
	}
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) && isLocalAddr(addr) {
			if who := busyHolder(c.Port()); who != "" {
				return sessionErr("desk_busy", "%s accepts no further client: rc_audio_mixer serves one client at a time and %s holds it", addr, who)
			}
		}
		return sessionErr("connect_failed", "connecting to %s: %w", addr, err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(1 << 20) // never let the desk block on us
		_ = tc.SetNoDelay(true)
	}
	c.mu.Lock()
	c.conn = conn
	c.stats.Sessions++
	c.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		stop()
		_ = conn.Close()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()
	c.setState(StateSyncing, "", "")
	ownLocal := conn.LocalAddr().String()

	fr := rcproto.NewFrameReader(conn)
	var gotSync atomic.Bool
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.SyncTimeout))
	switch c.cfg.Handshake {
	case "always":
		c.sendHandshake(conn)
	case "never":
	default: // auto: only if no full sync arrives in time (other messages may)
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-done:
			case <-time.After(c.handshakeAfter()):
				if !gotSync.Load() {
					c.cfg.Logf("desk: no full sync after connect, sending session-open handshake")
					c.sendHandshake(conn)
				}
			}
		}()
	}

	synced := false
	conflict := make(chan struct{})
	ended := make(chan struct{}) // the guard check is moot once the session is over
	defer close(ended)
	var checked atomic.Bool // the delayed guard check has run
	for {
		body, err := fr.Next()
		if err != nil {
			select {
			case <-conflict:
				return errConflict
			default:
			}
			// Dropped before the guard check ran: a single-client desk may
			// have pushed us out because the GUI reconnected quickly. Check
			// now, or each retry would displace the GUI again.
			if before != nil && synced && !checked.Load() {
				if lost := c.lostPeers(before, ownLocal); len(lost) > 0 {
					c.cfg.Logf("desk: guard: client(s) %v vanished while we were connected; staying away", lost)
					return errConflict
				}
			}
			if !synced {
				return sessionErr("no_full_sync", "no complete device state received: %w", err)
			}
			return sessionErr("disconnected", "connection closed: %w", err)
		}
		c.mu.Lock()
		c.stats.FramesIn++
		c.stats.BytesIn += uint64(len(body)) + 8
		resyncs := fr.Resyncs
		c.mu.Unlock()
		if synced && resyncs > 0 {
			return c.desync("lost frame boundary (%d bytes skipped)", resyncs)
		}
		if len(body) > 0 && (body[0] < byte(rcproto.PropertyChanged) || body[0] > byte(rcproto.PropertyRemoved)) {
			// Not a ValueTreeSynchroniser message at all; it cannot affect the tree.
			c.mu.Lock()
			c.stats.UnknownTypes++
			c.mu.Unlock()
			continue
		}
		ch, err := rcproto.DecodeChange(body)
		if err != nil {
			c.mu.Lock()
			c.stats.DecodeErrors++
			c.mu.Unlock()
			if synced {
				return c.desync("undecodable message: %v", err)
			}
			continue
		}
		if !synced && ch.Type != rcproto.FullSync {
			continue // nothing to apply it to yet
		}
		if err := c.mirror.Ingest(ch); err != nil {
			c.mu.Lock()
			c.stats.ApplyErrors++
			c.mu.Unlock()
			if ch.Type == rcproto.FullSync || synced {
				return c.desync("change does not apply to the mirror: %v", err)
			}
			continue
		}
		if ch.Type != rcproto.FullSync {
			continue
		}
		c.mu.Lock()
		c.stats.FullSyncs++
		c.mu.Unlock()
		if synced {
			continue
		}
		synced = true
		gotSync.Store(true)
		fr.Resyncs = 0 // junk before the first full sync is harmless
		_ = conn.SetReadDeadline(time.Time{})
		c.setState(StateLive, "", "")
		nodes, props := 0, 0
		c.mirror.View(func(s Snapshot) { nodes, props = s.Root.Count() })
		c.cfg.Logf("desk: live, full sync %d bytes, %d nodes, %d properties", len(body), nodes, props)
		if before != nil {
			go c.guardCheck(time.After(c.cfg.GuardSettle), ended, before, ownLocal, conn, conflict, &checked)
		}
	}
}

func (c *Client) desync(format string, args ...interface{}) error {
	c.mu.Lock()
	c.stats.Desyncs++
	c.mu.Unlock()
	err := sessionErr("desync", "mirror out of sync, reconnecting for a fresh full sync: "+format, args...)
	c.cfg.Logf("desk: %v", err)
	return err
}

func (c *Client) handshakeAfter() time.Duration {
	if d := c.cfg.SyncTimeout / 5; d < 3*time.Second {
		return d
	}
	return 3 * time.Second
}

// sendHandshake writes the session-open body the desk expects on USB. On TCP
// it is normally unnecessary; the desk ignores message types it does not
// know, so sending it is harmless.
func (c *Client) sendHandshake(conn net.Conn) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write(rcproto.Frame(rcproto.SessionOpen))
}

// guardCheck runs once after the settle delay and kills the session if a
// pre-existing client of the mixer lost its connection when we joined.
func (c *Client) guardCheck(at <-chan time.Time, ended <-chan struct{}, before map[string]bool, own string, conn net.Conn, conflict chan struct{}, checked *atomic.Bool) {
	select {
	case <-at:
	case <-ended:
		return
	}
	lost := c.lostPeers(before, own)
	checked.Store(true)
	if len(lost) > 0 {
		c.cfg.Logf("desk: guard: client(s) %v vanished after we connected; disconnecting", lost)
		close(conflict)
		_ = conn.Close()
	}
}

// lostPeers lists connections to the mixer that existed before we joined and
// were dropped by the mixer since. A client that left by itself (it closed
// its end first) was not displaced by us.
func (c *Client) lostPeers(before map[string]bool, own string) []string {
	after, err := serverPeers(c.Port())
	if err != nil {
		return nil
	}
	var lost []string
	for peer := range before {
		if peer == own || after[peer] {
			continue
		}
		if closedBy(c.Port(), peer) == "client" {
			c.cfg.Logf("desk: guard: client %s left by itself", peer)
			continue
		}
		lost = append(lost, peer)
	}
	return lost
}

// Write sends one property change to the desk. build constructs (and
// validates) the change from the current tree; validation, sending and the
// local update happen under the mirror lock, so the path cannot be shifted by
// a concurrent structural change. epoch must match the caller's view.
func (c *Client) Write(epoch uint64, build func(s Snapshot) (*rcproto.Change, error)) (*rcproto.Change, error) {
	if c.cfg.ReadOnly {
		return nil, ErrReadOnly
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	conn, state := c.conn, c.state
	c.mu.Unlock()
	if conn == nil || state != StateLive {
		return nil, ErrOffline
	}
	return c.mirror.commit(epoch, build, func(ch *rcproto.Change) error {
		if ch.Type != rcproto.PropertyChanged {
			return sdk.NewError(http.StatusBadRequest, "invalid_change", "only property changes can be written")
		}
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(rcproto.Frame(ch.Encode())); err != nil {
			_ = conn.Close()
			return sdk.NewError(http.StatusServiceUnavailable, "send_failed", "sending to the desk failed: "+err.Error())
		}
		c.mu.Lock()
		c.stats.FramesOut++
		c.mu.Unlock()
		return nil
	})
}
