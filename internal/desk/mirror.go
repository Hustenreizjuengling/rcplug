// Package desk keeps a live mirror of the RØDECaster's state tree by talking
// to rc_audio_mixer's control port (TCP 2345): it connects, receives the full
// sync, applies every change, protects the device's own GUI connection, and
// writes property changes back atomically with respect to the mirror.
//
// The HTTP API on top of it lives in package console.
package desk

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Errors returned by the mirror and the client. They carry stable codes for
// API clients (see sdk.APIError).
var (
	ErrNotSynced  = sdk.NewError(http.StatusServiceUnavailable, "not_synced", "no device state received yet")
	ErrStaleEpoch = sdk.NewError(http.StatusConflict, "stale_epoch", "the device state changed structurally; reload it and retry")
	ErrOffline    = sdk.NewError(http.StatusServiceUnavailable, "offline", "not connected to the desk")
	ErrReadOnly   = sdk.NewError(http.StatusForbidden, "read_only", "writes are disabled (--read-only)")
	ErrPassive    = sdk.NewError(http.StatusForbidden, "passive", "the console reads along with the device's screen and cannot change the desk; use the device buttons (Display page)")
	ErrTooMany    = sdk.NewError(http.StatusServiceUnavailable, "too_many_clients", "too many live connections, try again later")
)

// MaxSubscribers bounds concurrent live (SSE) clients.
const MaxSubscribers = 32

// Mirror is the local copy of the device tree. Every change gets a sequence
// number; structural changes (and full syncs) bump the epoch, which
// invalidates index paths held by web clients.
type Mirror struct {
	mu     sync.Mutex
	root   *rcproto.Node
	layout *rcproto.Layout
	epoch  uint64
	seq    uint64
	stale  bool // last known state, not live (disconnected or resyncing)

	// pending collects property changes since the last broadcast, coalesced
	// so a fast meter stream costs at most one entry per property per tick.
	pending map[string]int // key -> index into batch
	batch   []pendingChange
	resync  bool

	subs map[*Subscriber]struct{}

	changes  uint64
	lastRate float64
	rateAt   time.Time
	rateBase uint64
}

type pendingChange struct {
	path []int
	name string
	v    rcproto.Value
}

// Subscriber receives broadcast messages (SSE events).
type Subscriber struct {
	C chan Message
	// lost is set when a message had to be dropped; the next delivery is a
	// resync so the client refetches the whole tree instead of drifting.
	lost bool
}

// Message is one event for web clients.
type Message struct {
	Event string // "changes" | "resync" | "status" | "control"
	Data  []byte // JSON
}

// NewMirror returns an empty mirror (no device state yet).
//
// The epoch starts at a random value, so a browser that still holds the tree
// of a previous console process (restart, update, reboot) sees a different
// epoch and reloads, and its old paths are refused. It stays below 2^53 so
// JavaScript numbers represent it exactly.
func NewMirror() *Mirror {
	return &Mirror{pending: map[string]int{}, subs: map[*Subscriber]struct{}{}, rateAt: time.Now(),
		epoch: rand.Uint64N(1 << 40)}
}

// Ready reports whether a full sync has been received at some point.
func (m *Mirror) Ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.root != nil
}

// SetStale marks the tree as last-known (true) or live (false). A stale tree
// is still served for display but flagged as such.
func (m *Mirror) SetStale(stale bool) {
	m.mu.Lock()
	m.stale = stale
	m.mu.Unlock()
}

// Ingest applies one change received from the device.
func (m *Mirror) Ingest(c *rcproto.Change) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Type == rcproto.FullSync {
		m.stale = false
	}
	return m.applyLocked(c)
}

func (m *Mirror) applyLocked(c *rcproto.Change) error {
	if c.Type != rcproto.FullSync && m.root == nil {
		return ErrNotSynced
	}
	root, err := rcproto.Apply(m.root, c)
	if err != nil {
		return err
	}
	m.root = root
	m.seq++
	m.changes++
	if c.Structural() {
		m.layout = rcproto.DiscoverLayout(m.root)
		m.epoch++
		m.resetPendingLocked()
		return nil
	}
	key := pathKey(c.Path) + "\x00" + c.Name
	v := c.Value
	if c.Type == rcproto.PropertyRemoved {
		v = rcproto.Void()
	}
	if i, ok := m.pending[key]; ok {
		m.batch[i].v = v
	} else {
		m.pending[key] = len(m.batch)
		m.batch = append(m.batch, pendingChange{path: append([]int(nil), c.Path...), name: c.Name, v: v})
	}
	return nil
}

func (m *Mirror) resetPendingLocked() {
	m.pending = map[string]int{}
	m.batch = m.batch[:0]
	m.resync = true
}

func pathKey(p []int) string {
	var b []byte
	for i, x := range p {
		if i > 0 {
			b = append(b, '.')
		}
		b = strconv.AppendInt(b, int64(x), 10)
	}
	return string(b)
}

// Snapshot describes the mirror for one View call.
type Snapshot struct {
	Root   *rcproto.Node // nil before the first full sync; do not retain
	Layout *rcproto.Layout
	Epoch  uint64
	Seq    uint64
	Stale  bool
}

// View runs fn with the current tree under the mirror lock. fn must be quick
// and must not retain the tree.
func (m *Mirror) View(fn func(s Snapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(Snapshot{Root: m.root, Layout: m.layout, Epoch: m.epoch, Seq: m.seq, Stale: m.stale})
}

// commit validates and applies a local write atomically: under the mirror
// lock it checks the epoch, lets build construct the change from the current
// tree, hands it to send, and applies it locally once sent (the desk does not
// echo a client's own writes). No device change can slip in between.
func (m *Mirror) commit(epoch uint64, build func(s Snapshot) (*rcproto.Change, error), send func(*rcproto.Change) error) (*rcproto.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root == nil {
		return nil, ErrNotSynced
	}
	if m.stale {
		return nil, ErrOffline
	}
	if epoch != m.epoch {
		return nil, ErrStaleEpoch
	}
	ch, err := build(Snapshot{Root: m.root, Layout: m.layout, Epoch: m.epoch, Seq: m.seq})
	if err != nil {
		return nil, err
	}
	if err := send(ch); err != nil {
		return nil, err
	}
	return ch, m.applyLocked(ch)
}

// Overlay applies readings that do not come over the control port (the
// meters and fader positions the mixer keeps in shared memory, see package
// hw). fn builds property changes from the current tree under the mirror
// lock, so its paths cannot go stale in between; values that did not change
// are skipped. It does nothing before the first full sync.
func (m *Mirror) Overlay(fn func(s Snapshot) []rcproto.Change) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root == nil {
		return
	}
	for _, c := range fn(Snapshot{Root: m.root, Layout: m.layout, Epoch: m.epoch, Seq: m.seq, Stale: m.stale}) {
		if c.Type != rcproto.PropertyChanged {
			continue
		}
		n := m.root.At(c.Path)
		if n == nil {
			continue
		}
		if old, ok := n.Get(c.Name); ok && old.Equal(c.Value) {
			continue
		}
		_ = m.applyLocked(&c)
	}
}

// Subscribed reports whether a live client is connected.
func (m *Mirror) Subscribed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs) > 0
}

// Rate returns property changes per second (averaged over the last second).
func (m *Mirror) Rate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := time.Since(m.rateAt); d > time.Second {
		m.lastRate = float64(m.changes-m.rateBase) / d.Seconds()
		m.rateAt, m.rateBase = time.Now(), m.changes
	}
	return m.lastRate
}

// Subscribe registers a live client. Call Unsubscribe when done.
func (m *Mirror) Subscribe() (*Subscriber, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) >= MaxSubscribers {
		return nil, ErrTooMany
	}
	s := &Subscriber{C: make(chan Message, 32)}
	m.subs[s] = struct{}{}
	return s, nil
}

// Unsubscribe removes a subscriber.
func (m *Mirror) Unsubscribe(s *Subscriber) {
	m.mu.Lock()
	delete(m.subs, s)
	m.mu.Unlock()
}

// Broadcast publishes pending changes (or a resync notice) to all
// subscribers. It never blocks: a subscriber that falls behind is told to
// resync instead. Call it periodically.
func (m *Mirror) Broadcast(secret SecretFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) == 0 {
		m.pending = map[string]int{}
		m.batch = m.batch[:0]
		m.resync = false
		return
	}
	resyncData, _ := json.Marshal(map[string]uint64{"epoch": m.epoch, "seq": m.seq})
	var msg *Message
	switch {
	case m.resync:
		msg = &Message{Event: "resync", Data: resyncData}
		m.resync = false
	case len(m.batch) > 0:
		msg = &Message{Event: "changes", Data: m.encodeBatchLocked(secret)}
	}
	m.pending = map[string]int{}
	m.batch = m.batch[:0]
	for s := range m.subs {
		var out Message
		switch {
		case s.lost:
			out = Message{Event: "resync", Data: resyncData} // even on a quiet desk
		case msg != nil:
			out = *msg
		default:
			continue
		}
		select {
		case s.C <- out:
			s.lost = false
		default:
			s.lost = true
		}
	}
}

// encodeBatchLocked renders {"epoch":E,"seq":S,"ch":[[[path],name,value,type],…]}.
func (m *Mirror) encodeBatchLocked(secret SecretFunc) []byte {
	var b bytes.Buffer
	b.WriteString(`{"epoch":`)
	b.WriteString(strconv.FormatUint(m.epoch, 10))
	b.WriteString(`,"seq":`)
	b.WriteString(strconv.FormatUint(m.seq, 10))
	b.WriteString(`,"ch":[`)
	for i, pc := range m.batch {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`[[`)
		for j, x := range pc.path {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(x))
		}
		b.WriteString(`],`)
		writeJSONString(&b, pc.name)
		b.WriteByte(',')
		typ := ""
		if n := m.root.At(pc.path); n != nil {
			typ = n.Type
		}
		s := secret != nil && secret(typ, pc.name)
		WriteValue(&b, pc.v, s, true)
		b.WriteString(`,"`)
		b.WriteByte(TypeCode(pc.v, s))
		b.WriteString(`"]`)
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

// Notify sends an out-of-band event (e.g. connection status) to everyone.
func (m *Mirror) Notify(event string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range m.subs {
		select {
		case s.C <- Message{Event: event, Data: data}:
		default:
			s.lost = true
		}
	}
}
