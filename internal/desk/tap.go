package desk

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// Reading along ("tap"). On the Duo, rc_audio_mixer serves one client at a
// time and the device's screen (rc_gui_juce) holds that connection from boot,
// so the console cannot connect. Instead it reads the screen's connection on
// the loopback interface: the full sync the mixer sends when the screen
// connects, every change the mixer sends it, and every change the screen sends
// the mixer (the mixer does not echo those). It never sends anything, so it
// cannot disturb the desk; it only sees a full sync when the screen connects
// (at boot, or when the screen restarts).

type tapDir int

const (
	fromDesk tapDir = iota // the mixer to its client
	toDesk                 // the client to the mixer
)

// TCP flags.
const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
)

const (
	// tapMaxAhead bounds the bytes kept beyond a gap (segments that arrived
	// before a missing one); more, or a gap older than tapGapTimeout, means a
	// packet was lost for good.
	tapMaxAhead   = 4 << 20
	tapGapTimeout = 2 * time.Second
)

// tapStream reassembles one direction of a TCP connection.
type tapStream struct {
	started bool
	next    uint32            // sequence number of the next byte expected
	buf     []byte            // received bytes not yet split into frames
	ahead   map[uint32][]byte // segments beyond a gap, by sequence number
	aheadN  int
	gapAt   time.Time
	lost    bool
	// aligning: joined mid-stream (resuming), the first bytes may be the
	// middle of a message; skip to the next frame header.
	aligning bool
}

// add takes a segment; it returns false once bytes are missing for good.
func (s *tapStream) add(seq uint32, p []byte, now time.Time) bool {
	if s.lost {
		return false
	}
	if !s.started { // joined mid-stream: nothing before this can be had
		s.started, s.next = true, seq
	}
	switch d := int32(seq - s.next); {
	case d <= 0: // in order, or overlapping what we have (a retransmission)
		if back := -int64(d); back < int64(len(p)) { // int64: -MinInt32 does not fit an int32
			s.buf = append(s.buf, p[back:]...)
			s.next = seq + uint32(len(p))
			s.drainAhead()
		}
	default: // a gap: keep this for when the missing bytes come
		if s.ahead == nil {
			s.ahead = map[uint32][]byte{}
		}
		if _, dup := s.ahead[seq]; !dup {
			s.ahead[seq] = append([]byte(nil), p...)
			s.aheadN += len(p)
		}
		if s.gapAt.IsZero() {
			s.gapAt = now
		}
	}
	if len(s.ahead) > 0 && (s.aheadN > tapMaxAhead || now.Sub(s.gapAt) > tapGapTimeout) {
		s.lost = true
		return false
	}
	return true
}

func (s *tapStream) drainAhead() {
	for progress := true; progress && len(s.ahead) > 0; {
		progress = false
		for seq, p := range s.ahead {
			d := int32(seq - s.next)
			if d > 0 {
				continue
			}
			delete(s.ahead, seq)
			s.aheadN -= len(p)
			if back := -int64(d); back < int64(len(p)) {
				s.buf = append(s.buf, p[back:]...)
				s.next = seq + uint32(len(p))
			}
			progress = true
		}
	}
	if len(s.ahead) == 0 {
		s.gapAt = time.Time{}
	}
}

// tapConn is one connection between the mixer and a client.
type tapConn struct {
	client string // the client's address
	s      [2]tapStream
	synced bool // the mixer's full sync for this connection was applied
	late   bool // seen without its start: no full sync will come
}

// resumeState is kept across a restart of the console (an update, a restart
// of the plugin): the mirrored tree and the screen's connection it belongs
// to. If that connection is still there, reading along goes on from it.
type resumeState struct {
	Client string    `json:"client"`
	Boot   string    `json:"boot"`
	Saved  time.Time `json:"saved"`
	tree   *rcproto.Node
}

// resumeMaxAge: a kept state older than this is not used.
const resumeMaxAge = 10 * time.Minute

// bootIDFile identifies this boot of the machine (tests replace it).
var bootIDFile = "/proc/sys/kernel/random/boot_id"

func bootID() string {
	b, _ := os.ReadFile(bootIDFile)
	return strings.TrimSpace(string(b))
}

// saveResume keeps the tree and the connection in path.json and path.frame
// (root only: the tree is the whole device state).
func saveResume(path, client string, root *rcproto.Node, now time.Time) error {
	meta, err := json.Marshal(resumeState{Client: client, Boot: bootID(), Saved: now})
	if err != nil {
		return err
	}
	frame := rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: root}).Encode())
	for name, data := range map[string][]byte{path + ".frame": frame, path + ".json": meta} {
		tmp := name + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, name); err != nil {
			return err
		}
	}
	return nil
}

// loadResume reads a kept state once (the files are removed): only from this
// boot and not older than resumeMaxAge.
func loadResume(path string, now time.Time) (*resumeState, error) {
	meta, err := os.ReadFile(path + ".json")
	frame, ferr := os.ReadFile(path + ".frame")
	os.Remove(path + ".json")
	os.Remove(path + ".frame")
	if err != nil {
		return nil, err
	}
	if ferr != nil {
		return nil, ferr
	}
	var r resumeState
	if err := json.Unmarshal(meta, &r); err != nil {
		return nil, err
	}
	if r.Boot == "" || r.Boot != bootID() {
		return nil, errors.New("kept state is from another boot")
	}
	if age := now.Sub(r.Saved); age < 0 || age > resumeMaxAge {
		return nil, fmt.Errorf("kept state is %s old", age.Round(time.Second))
	}
	body, _, err := rcproto.ParseFrame(frame)
	if err != nil {
		return nil, err
	}
	ch, err := rcproto.DecodeChange(body)
	if err != nil || ch.Type != rcproto.FullSync || ch.Tree == nil {
		return nil, errors.New("kept state is not a full sync")
	}
	r.tree = ch.Tree
	return &r, nil
}

// tapEvent tells the client what the tapper saw.
type tapEvent struct {
	kind   string // "live", "ended", "lost"
	detail string
	bytes  int // size of the full sync ("live")
}

// tapper turns captured IPv4/TCP packets of the mixer's port into changes of
// the mirror. Not safe for concurrent use: one goroutine feeds it.
type tapper struct {
	port    int
	mirror  *Mirror
	event   func(tapEvent)
	conns   map[string]*tapConn
	current *tapConn // the connection the mirror follows
	stats   *Stats
	now     func() time.Time
	resume  *resumeState // a kept state waiting for its connection
}

func newTapper(port int, m *Mirror, stats *Stats, event func(tapEvent)) *tapper {
	return &tapper{port: port, mirror: m, event: event, conns: map[string]*tapConn{}, stats: stats, now: time.Now}
}

// packet handles one IPv4 packet (starting at the IP header).
func (t *tapper) packet(b []byte) {
	if len(b) < 20 || b[0]>>4 != 4 || b[9] != 6 {
		return
	}
	ihl := int(b[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if ihl < 20 || total > len(b) || total < ihl+20 {
		return
	}
	if binary.BigEndian.Uint16(b[6:8])&0x3fff != 0 { // a fragment (never on lo)
		return
	}
	tcp := b[ihl:total]
	off := int(tcp[12]>>4) * 4
	if off < 20 || off > len(tcp) {
		return
	}
	sp, dp := int(binary.BigEndian.Uint16(tcp[0:2])), int(binary.BigEndian.Uint16(tcp[2:4]))
	var dir tapDir
	var client string
	switch {
	case dp == t.port:
		dir, client = toDesk, hostPort(b[12:16], sp)
	case sp == t.port:
		dir, client = fromDesk, hostPort(b[16:20], dp)
	default:
		return
	}
	t.segment(client, dir, binary.BigEndian.Uint32(tcp[4:8]), tcp[13], tcp[off:])
}

func hostPort(ip []byte, port int) string {
	return net.JoinHostPort(net.IP(ip).String(), strconv.Itoa(port))
}

func (t *tapper) segment(client string, dir tapDir, seq uint32, flags byte, payload []byte) {
	c := t.conns[client]
	if flags&tcpSYN != 0 {
		if dir == toDesk || c == nil { // a client opens a (new) connection
			if c != nil && c == t.current {
				t.end(c, "the connection was replaced")
			}
			c = &tapConn{client: client}
			t.conns[client] = c
		}
		s := &c.s[dir]
		*s = tapStream{started: true, next: seq + 1}
		return
	}
	if c == nil {
		if flags&(tcpFIN|tcpRST) != 0 || len(payload) == 0 {
			return
		}
		c = &tapConn{client: client, late: true}
		t.conns[client] = c
		if r := t.resume; r != nil && r.Client == client {
			// The connection the kept state belongs to: go on from there.
			t.resume = nil
			if err := t.mirror.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: r.tree}); err == nil {
				c.late, c.synced, t.current = false, true, c
				c.s[fromDesk].aligning, c.s[toDesk].aligning = true, true
				t.event(tapEvent{kind: "resumed"})
			}
		}
	}
	if len(payload) > 0 {
		if !c.s[dir].add(seq, payload, t.now()) {
			if c == t.current {
				t.lose(c, "packets were lost while reading along")
			}
			delete(t.conns, client)
			return
		}
		t.frames(c, dir)
	}
	if flags&(tcpFIN|tcpRST) != 0 {
		if c == t.current {
			t.end(c, "the screen's connection to the mixer ended")
		}
		delete(t.conns, client)
	}
}

var magic = binary.LittleEndian.AppendUint32(nil, rcproto.Magic)

// frames splits the stream into messages and applies them.
func (t *tapper) frames(c *tapConn, dir tapDir) {
	s := &c.s[dir]
	for len(s.buf) > 0 {
		body, n, err := rcproto.ParseFrame(s.buf)
		if err == rcproto.ErrShort {
			break
		}
		if err != nil { // lost the frame boundary: look for the next header
			if c == t.current && c.synced && !s.aligning {
				t.lose(c, "lost the frame boundary")
				return
			}
			i := bytes.Index(s.buf[1:], magic)
			if i < 0 {
				s.buf = append(s.buf[:0], s.buf[len(s.buf)-3:]...)
				break
			}
			s.buf = s.buf[1+i:]
			continue
		}
		s.buf = s.buf[n:]
		s.aligning = false
		t.message(c, dir, body)
	}
	if len(s.buf) == 0 {
		s.buf = s.buf[:0:0] // let a large full sync's buffer go
	}
}

func (t *tapper) message(c *tapConn, dir tapDir, body []byte) {
	t.stats.FramesIn++
	t.stats.BytesIn += uint64(len(body)) + 8
	if len(body) == 0 || body[0] < byte(rcproto.PropertyChanged) || body[0] > byte(rcproto.PropertyRemoved) {
		t.stats.UnknownTypes++
		return
	}
	ch, err := rcproto.DecodeChange(body)
	if err != nil {
		t.stats.DecodeErrors++
		if c == t.current && c.synced {
			t.lose(c, "an undecodable message")
		}
		return
	}
	if ch.Type == rcproto.FullSync {
		if dir == toDesk || c.late {
			return // the client's own idea of the tree; the mixer's counts
		}
		if err := t.mirror.Ingest(ch); err != nil {
			t.stats.ApplyErrors++
			return
		}
		t.stats.FullSyncs++
		c.synced, t.current = true, c
		t.event(tapEvent{kind: "live", bytes: len(body)})
		return
	}
	if c != t.current || !c.synced {
		return
	}
	if err := t.mirror.Ingest(ch); err != nil {
		t.stats.ApplyErrors++
		if ch.Structural() {
			t.lose(c, "a change that does not fit the mirrored tree")
		}
	}
}

// end: the followed connection is over; the mirror keeps the last state.
func (t *tapper) end(c *tapConn, why string) {
	t.current = nil
	c.synced = false
	t.mirror.SetStale(true)
	t.event(tapEvent{kind: "ended", detail: why})
}

// lose: the mirror can no longer follow this connection.
func (t *tapper) lose(c *tapConn, why string) {
	t.stats.Desyncs++
	t.current = nil
	c.synced = false
	t.mirror.SetStale(true)
	t.event(tapEvent{kind: "lost", detail: why})
}

// runTap reads along with the device's screen until ctx ends.
func (c *Client) runTap(ctx context.Context) {
	for ctx.Err() == nil {
		cp, err := openCapture(c.Port())
		if err != nil {
			c.setState(StateBackoff, "tap_failed", err.Error())
			c.cfg.Logf("desk: %v", err)
			if !c.sleepOrKick(ctx, 30*time.Second) {
				return
			}
			continue
		}
		c.setState(StateWaiting, "tap_waiting", "reading along with the device's screen; the state arrives when the screen connects to the mixer")
		c.cfg.Logf("desk: reading along on port %d (the mixer serves one client: the device's screen)", c.Port())
		c.mu.Lock()
		stats := c.stats
		c.mu.Unlock()
		tp := newTapper(c.Port(), c.mirror, &stats, c.tapEvent)
		if c.cfg.ResumeFile != "" {
			if r, err := loadResume(c.cfg.ResumeFile, time.Now()); err == nil {
				tp.resume = r
				c.cfg.Logf("desk: kept state found (from %s); going on when the screen's connection %s is seen", r.Saved.Format("15:04:05"), r.Client)
			} else if !errors.Is(err, os.ErrNotExist) {
				c.cfg.Logf("desk: kept state not used: %v", err)
			}
		}
		for ctx.Err() == nil {
			pkt, err := cp.next()
			if err != nil {
				c.cfg.Logf("desk: reading along: %v", err)
				break
			}
			if pkt == nil {
				continue
			}
			tp.packet(pkt)
			c.mu.Lock()
			c.stats = stats
			c.mu.Unlock()
		}
		cp.close()
		if ctx.Err() != nil && c.cfg.ResumeFile != "" && tp.current != nil && tp.current.synced {
			var root *rcproto.Node
			c.mirror.View(func(s Snapshot) {
				if s.Root != nil {
					root = s.Root.Clone()
				}
			})
			if root != nil {
				if err := saveResume(c.cfg.ResumeFile, tp.current.client, root, time.Now()); err != nil {
					c.cfg.Logf("desk: keeping the state for a restart: %v", err)
				} else {
					c.cfg.Logf("desk: state kept for a restart")
				}
			}
		}
		c.mirror.SetStale(true)
		if ctx.Err() == nil && !c.sleepOrKick(ctx, 5*time.Second) {
			return
		}
	}
}

func (c *Client) tapEvent(e tapEvent) {
	switch e.kind {
	case "live":
		c.setState(StateLive, "", "")
		nodes, props := 0, 0
		c.mirror.View(func(s Snapshot) { nodes, props = s.Root.Count() })
		c.cfg.Logf("desk: live (reading along), full sync %d bytes, %d nodes, %d properties", e.bytes, nodes, props)
	case "resumed":
		c.setState(StateLive, "resumed", "went on with the state kept at the last stop; changes made while the console was not running may be missing")
		c.cfg.Logf("desk: live (reading along), went on with the state kept at the last stop")
	case "ended":
		c.setState(StateWaiting, "tap_waiting", e.detail+"; waiting for it to reconnect")
		c.cfg.Logf("desk: %s; showing the last known state", e.detail)
	default:
		c.setState(StateWaiting, "tap_lost", "lost track ("+e.detail+"); the state comes back when the device's screen reconnects")
		c.cfg.Logf("desk: reading along lost track: %s", e.detail)
	}
}
