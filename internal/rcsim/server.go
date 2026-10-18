package rcsim

import (
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// Options configures the simulator.
type Options struct {
	// MeterHz is the meter update rate (0 = no meter traffic).
	MeterHz float64
	// Animate makes "someone" slowly move physical fader 1 now and then.
	Animate bool
	// Exclusive emulates a single-client server: a new connection drops all
	// existing ones. Used to exercise the client's GUI-protection guard.
	Exclusive bool
	// OneClient serves one client the way rc_audio_mixer 1.7.x does: the
	// listener closes while a client is connected and opens again on the
	// same address when it leaves (see RelistenFailures).
	OneClient bool
	// Ping sends a "ping" frame to every client at this interval, as the
	// desk does (0 = never).
	Ping time.Duration
	// WaitHandshake withholds the full sync until the client sends the
	// USB-style session-open body (to exercise the client's fallback).
	WaitHandshake bool
	// Logf receives diagnostic messages (defaults to log.Printf).
	Logf func(format string, args ...interface{})
}

// Server is a fake rc_audio_mixer control port.
type Server struct {
	opts Options

	mu       sync.Mutex
	root     *rcproto.Node
	layout   *rcproto.Layout
	clients  map[*client]struct{}
	writes   []rcproto.Change
	created  []string // properties a client created by writing unknown names
	rng      *rand.Rand
	meters   []float64
	recSince time.Time

	ln               net.Listener // guarded by mu
	addr             string
	relistenFailures int
	done             chan struct{}
	closeOnce        sync.Once
	wg               sync.WaitGroup
}

type client struct {
	conn   net.Conn
	out    chan []byte
	closed chan struct{} // closed with the connection; ends writeLoop
	once   sync.Once
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

// New creates a simulator serving root (which it takes ownership of).
func New(root *rcproto.Node, opts Options) *Server {
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	s := &Server{
		opts:    opts,
		root:    root,
		layout:  rcproto.DiscoverLayout(root),
		clients: map[*client]struct{}{},
		rng:     rand.New(rand.NewSource(1)),
		done:    make(chan struct{}),
	}
	s.meters = make([]float64, s.layout.Runs["METER"].Count)
	return s
}

// NewDuo returns a simulator loaded with the realistic Duo profile.
func NewDuo(opts Options) (*Server, error) { return NewFromFrame(DuoFrame, opts) }

// NewFromFrame returns a simulator serving a captured full-sync frame (see
// cmd/rccapture). The realistic profile fills in whatever of it the tree has.
func NewFromFrame(frame []byte, opts Options) (*Server, error) {
	root, err := TreeFromFrame(frame)
	if err != nil {
		return nil, err
	}
	MakeRealistic(root)
	return New(root, opts), nil
}

// Listen starts serving on addr (e.g. "127.0.0.1:2345", or ":0" for tests).
func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln, s.addr = ln, ln.Addr().String()
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop(ln)
	if s.opts.MeterHz > 0 || s.opts.Animate {
		s.wg.Add(1)
		go s.tickLoop()
	}
	if s.opts.Ping > 0 {
		s.wg.Add(1)
		go s.pingLoop()
	}
	return nil
}

// Addr returns the listening address (also while a OneClient server has
// its listener closed).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Listening reports whether the server accepts connections right now.
func (s *Server) Listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

// RelistenFailures counts the times a OneClient server could not listen
// again after its client left, because something else had taken its
// address. The real mixer would then never accept its screen again.
func (s *Server) RelistenFailures() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.relistenFailures
}

// relistenLocked opens the listener again after a OneClient server's client
// left.
func (s *Server) relistenLocked() {
	select {
	case <-s.done:
		return
	default:
	}
	if s.ln != nil {
		return
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.relistenFailures++
		s.opts.Logf("rcsim: cannot listen on %s again: %v", s.addr, err)
		return
	}
	s.ln = ln
	s.wg.Add(1)
	go s.acceptLoop(ln)
}

func (s *Server) pingLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.opts.Ping)
	defer t.Stop()
	ping := rcproto.Frame([]byte("ping"))
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			for c := range s.clients {
				s.sendLocked(c, ping)
			}
			s.mu.Unlock()
		}
	}
}

// Close stops the server and disconnects all clients.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		if s.ln != nil {
			_ = s.ln.Close()
			s.ln = nil
		}
		for c := range s.clients {
			c.close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
}

// Clients returns the number of connected clients.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Snapshot returns a deep copy of the simulated device state.
func (s *Server) Snapshot() *rcproto.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Clone()
}

// Writes returns the property writes received from clients so far.
func (s *Server) Writes() []rcproto.Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rcproto.Change(nil), s.writes...)
}

// Created lists properties that clients created by writing names the node
// did not have — something a well-behaved client must never do.
func (s *Server) Created() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.created...)
}

// Inject applies a device-originated change and broadcasts it to all clients.
func (s *Server) Inject(c *rcproto.Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(c, nil)
}

func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if s.opts.OneClient && errors.Is(err, net.ErrClosed) {
				return
			}
			s.opts.Logf("rcsim: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		c := &client{conn: conn, out: make(chan []byte, 8192), closed: make(chan struct{})}
		s.mu.Lock()
		if s.opts.OneClient {
			// Like the mixer's connectionMade: stop accepting.
			_ = ln.Close()
			if s.ln == ln {
				s.ln = nil
			}
		}
		if s.opts.Exclusive {
			for old := range s.clients {
				s.opts.Logf("rcsim: exclusive mode, dropping %s", old.conn.RemoteAddr())
				old.close()
				delete(s.clients, old)
			}
		}
		s.clients[c] = struct{}{}
		if !s.opts.WaitHandshake {
			s.sendFullLocked(c)
		}
		s.mu.Unlock()
		s.opts.Logf("rcsim: client %s connected", conn.RemoteAddr())
		s.wg.Add(2)
		go s.writeLoop(c)
		go s.readLoop(c)
		if s.opts.OneClient {
			return
		}
	}
}

func (s *Server) sendFullLocked(c *client) {
	s.sendLocked(c, rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: s.root}).Encode()))
}

func (s *Server) writeLoop(c *client) {
	defer s.wg.Done()
	defer c.close()
	for {
		select {
		case <-s.done:
			return
		case <-c.closed:
			return
		case b, ok := <-c.out:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := c.conn.Write(b); err != nil {
				return
			}
		}
	}
}

func (s *Server) readLoop(c *client) {
	defer s.wg.Done()
	defer func() {
		c.close()
		s.mu.Lock()
		delete(s.clients, c)
		if s.opts.OneClient && len(s.clients) == 0 {
			s.relistenLocked() // like the mixer's connectionLost
		}
		s.mu.Unlock()
		s.opts.Logf("rcsim: client %s disconnected", c.conn.RemoteAddr())
	}()
	fr := rcproto.NewFrameReader(c.conn)
	for {
		body, err := fr.Next()
		if err != nil {
			return
		}
		if string(body) == "ping" {
			continue
		}
		if string(body) == string(rcproto.SessionOpen) {
			s.opts.Logf("rcsim: session-open handshake from %s", c.conn.RemoteAddr())
			s.mu.Lock()
			s.sendFullLocked(c)
			s.mu.Unlock()
			continue
		}
		ch, err := rcproto.DecodeChange(body)
		if err != nil {
			s.opts.Logf("rcsim: bad frame from client: %v", err)
			continue
		}
		s.mu.Lock()
		s.handleClientLocked(c, ch)
		s.mu.Unlock()
	}
}

// send queues a frame for c without ever blocking the simulator.
func (s *Server) sendLocked(c *client, frame []byte) {
	select {
	case c.out <- frame:
	default:
		s.opts.Logf("rcsim: client %s too slow, dropping", c.conn.RemoteAddr())
		c.close()
		delete(s.clients, c)
	}
}

// applyLocked applies a change and broadcasts it to every client except skip.
func (s *Server) applyLocked(ch *rcproto.Change, skip *client) error {
	root, err := rcproto.Apply(s.root, ch)
	if err != nil {
		return err
	}
	s.root = root
	if ch.Structural() {
		s.layout = rcproto.DiscoverLayout(s.root)
	}
	frame := rcproto.Frame(ch.Encode())
	for c := range s.clients {
		if c != skip {
			s.sendLocked(c, frame)
		}
	}
	return nil
}

func (s *Server) setLocked(path []int, name string, v rcproto.Value) {
	_ = s.applyLocked(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: name, Value: v}, nil)
}

func (s *Server) handleClientLocked(c *client, ch *rcproto.Change) {
	if ch.Type != rcproto.PropertyChanged {
		s.opts.Logf("rcsim: ignoring client %s", ch.Type)
		return
	}
	s.writes = append(s.writes, *ch)
	n := s.root.At(ch.Path)
	if n == nil {
		s.opts.Logf("rcsim: write to missing node %v.%s ignored", ch.Path, ch.Name)
		return
	}
	if !n.Has(ch.Name) {
		// The real desk silently creates the property (and keeps it).
		s.created = append(s.created, fmt.Sprintf("%s%v.%s", n.Type, ch.Path, ch.Name))
		s.opts.Logf("rcsim: WARNING client created property %s.%s", n.Type, ch.Name)
	}
	// The desk does not echo a write to the client that made it.
	if err := s.applyLocked(ch, c); err != nil {
		s.opts.Logf("rcsim: %v", err)
		return
	}
	s.reactLocked(n, ch)
}

// reactLocked emulates the desk's side effects of a client write.
func (s *Server) reactLocked(n *rcproto.Node, ch *rcproto.Change) {
	switch {
	case n.Type == "RECORDER" && ch.Name == "requestRecordState":
		st := ch.Value.I
		if st == 2 {
			s.recSince = time.Now()
		}
		s.setLocked(ch.Path, "recordState", rcproto.Int(st))
		if st == 0 {
			s.setLocked(ch.Path, "recordTimeMs", rcproto.Int(0))
		}
	case n.Type == "MIX" && (ch.Name == "mixLinkRequest" || ch.Name == "mixUnlinkRequest"):
		if ch.Value.Kind == rcproto.KindBinary && string(ch.Value.B) == string(rcproto.TriggerPress) {
			s.setLocked(ch.Path, "mixLink", rcproto.Bool(ch.Name == "mixLinkRequest"))
			s.setLocked(ch.Path, ch.Name, rcproto.Binary(rcproto.TriggerRelease))
		}
	case n.Type == "PADBUTTON" && ch.Name == "padButtonPressed" && ch.Value.Truth():
		slot := s.layout.Runs["PADBUTTON"].IndexOf(ch.Path)
		bank := int64(0)
		if gui := s.layout.Node(s.root, "GUI", 0); gui != nil {
			v, _ := gui.Get("selectedBank")
			bank = v.I
		}
		idx := int(bank)*s.layout.PadsPerBank + slot
		pads := s.layout.Runs["SOUNDPADS/PAD"]
		for i := 0; i < pads.Count; i++ {
			p := s.root.At(pads.Path(i))
			if v, _ := p.Get("padIdx"); int(v.I) == idx {
				path := pads.Path(i)
				s.setLocked(path, "padActive", rcproto.Bool(true))
				go func() {
					time.Sleep(1500 * time.Millisecond)
					s.mu.Lock()
					s.setLocked(path, "padActive", rcproto.Bool(false))
					s.mu.Unlock()
				}()
			}
		}
	case n.Type == "FADER" && ch.Name == "faderLevel":
		s.faderMovedLocked(s.layout.Runs["FADER"].IndexOf(ch.Path), ch.Value.I)
	case n.Type == "SYSTEM" && ch.Name == "powerOffRequest":
		s.opts.Logf("rcsim: client requested power off (simulator ignores it)")
	}
}

// faderMovedLocked mirrors a fader position into its METER passthrough and the
// strip's linked routing cells, as the desk does.
func (s *Server) faderMovedLocked(strip int, level int64) {
	if strip < 0 {
		return
	}
	// The strip's meter mirrors the fader where the fixture has that
	// property; the simulator never adds properties the desk does not have.
	if p := s.layout.StripMeters.Path(strip); p != nil {
		if m := s.root.At(p); m != nil && m.Has("faderLevel") {
			s.setLocked(p, "faderLevel", rcproto.Int(level))
		}
	}
	ch := s.layout.Node(s.root, "CHANNEL", strip)
	if ch == nil {
		return
	}
	src, _ := ch.Get("channelInputSource")
	if src.I < 0 {
		return
	}
	lv := float64(level) / 127
	for o := 0; o < rcproto.MixPerSource; o++ {
		path := s.layout.MixPath(int(src.I), o)
		cell := s.root.At(path)
		if cell == nil {
			continue
		}
		if link, _ := cell.Get("mixLink"); !link.Truth() {
			continue
		}
		s.setLocked(path, "mixLevelWithAnchor", rcproto.String(fmt.Sprintf("%.6f|%.6f", lv, lv)))
	}
}

func (s *Server) tickLoop() {
	defer s.wg.Done()
	hz := s.opts.MeterHz
	if hz <= 0 {
		hz = 5
	}
	t := time.NewTicker(time.Duration(float64(time.Second) / hz))
	defer t.Stop()
	start := time.Now()
	lastFader := int64(-1)
	for {
		select {
		case <-s.done:
			return
		case now := <-t.C:
			s.mu.Lock()
			if s.opts.MeterHz > 0 {
				s.metersLocked()
			}
			if s.opts.Animate {
				// Glide fader 1 between 70 and 100 every ~20 s.
				ph := now.Sub(start).Seconds() / 20 * 2 * math.Pi
				lv := int64(85 + 15*math.Sin(ph))
				if lv != lastFader {
					lastFader = lv
					if p := s.layout.Path("FADER", 0); p != nil {
						s.setLocked(p, "faderLevel", rcproto.Int(lv))
						s.faderMovedLocked(0, lv)
					}
				}
			}
			if rec := s.layout.Node(s.root, "RECORDER", 0); rec != nil {
				if st, _ := rec.Get("recordState"); st.I == 2 {
					ms := time.Since(s.recSince).Milliseconds()
					if prev, _ := rec.Get("recordTimeMs"); ms/1000 != prev.I/1000 {
						s.setLocked(s.layout.Path("RECORDER", 0), "recordTimeMs", rcproto.Int(ms))
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

// metersLocked produces plausible speech-like meter activity on strips that
// have a source assigned and are not muted, sums them into the master and
// shows some gain reduction on loud strips, in the order of the desk's meter
// memory (strips, master, the channel open on the screen, gain reductions).
func (s *Server) metersLocked() {
	l := s.layout
	var sum float64
	for i := 0; i < l.StripMeters.Count; i++ {
		ch := l.Node(s.root, "CHANNEL", i)
		target := 0.0
		if ch != nil {
			src, _ := ch.Get("channelInputSource")
			mute, _ := ch.Get("channelOutputMute")
			if src.I >= 0 && !mute.Truth() {
				f := l.Node(s.root, "FADER", i)
				lv, _ := f.Get("faderLevel")
				target = (0.25 + 0.6*s.rng.Float64()) * math.Min(1, float64(lv.I)/rcproto.UnityFader)
				if s.rng.Float64() < 0.15 {
					target *= 0.1 // pauses between words
				}
			}
		}
		s.meters[i] = 0.6*s.meters[i] + 0.4*target
		sum += s.meters[i]
		s.meterPairLocked(l.StripMeters.Path(i), s.meters[i])
		s.meterMonoLocked(l.ReductionMeters.Path(i), reduction(s.meters[i]))
	}
	s.meterPairLocked(l.MasterMeter.Path(0), math.Min(1, sum/3*(0.9+0.1*s.rng.Float64())))
	// The simulated screen has the first channel open.
	if l.FocusMeters.Count == 3 && l.StripMeters.Count > 0 {
		s.meterPairLocked(l.FocusMeters.Path(0), math.Min(1, s.meters[0]*1.4))
		s.meterPairLocked(l.FocusMeters.Path(1), s.meters[0])
		s.meterMonoLocked(l.FocusMeters.Path(2), reduction(s.meters[0]))
	}
}

// reduction is a compressor's gain (1.0 = no reduction) for a level.
func reduction(v float64) float64 {
	if v <= 0.5 {
		return 1
	}
	return 1 - (v-0.5)*0.8
}

func (s *Server) meterMonoLocked(path []int, v float64) {
	if path == nil {
		return
	}
	for _, name := range []string{"meterLevelL", "meterLevelR", "meterPeakL", "meterPeakR"} {
		s.setLocked(path, name, rcproto.Double(v))
	}
}

func (s *Server) meterPairLocked(path []int, v float64) {
	if path == nil {
		return
	}
	l := v * (0.92 + 0.08*s.rng.Float64())
	r := v * (0.92 + 0.08*s.rng.Float64())
	s.setLocked(path, "meterLevelL", rcproto.Double(l))
	s.setLocked(path, "meterLevelR", rcproto.Double(r))
	s.setLocked(path, "meterPeakL", rcproto.Double(math.Min(1, l*1.15)))
	s.setLocked(path, "meterPeakR", rcproto.Double(math.Min(1, r*1.15)))
}
