package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// The event bus: plugins publish events through the host and subscribe to
// them (desk events from the console such as "desk.recording.started",
// "host.plugin.state" from the host, anything a plugin defines), so plugins
// work together without knowing each other's APIs.
//
// Each event carries its source, set by the host: a plugin proves who it is
// with the token the host hands it at start (RCPLUG_TOKEN), and "host." is
// reserved for the host itself. Browsers and the network can only read.
// The bus keeps the last events in memory (nothing on disk) and drops a
// subscriber that does not keep up, which then reconnects and catches up.

// Event is one message on the bus.
type Event struct {
	ID     uint64          `json:"id"`
	Time   time.Time       `json:"time"`
	Type   string          `json:"type"`
	Source string          `json:"source"`
	Data   json.RawMessage `json:"data,omitempty"`
}

const (
	eventsKept      = 200
	eventSubs       = 32
	eventDataMax    = 16 << 10
	eventSubBacklog = 256
)

var eventType = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*){1,5}$`)

// Bus is the host's event bus.
type Bus struct {
	mu     sync.Mutex
	next   uint64
	kept   []Event
	subs   map[*eventSub]struct{}
	tokens map[string]string // token → plugin name
}

type eventSub struct {
	prefixes []string
	c        chan Event
	lost     chan struct{}
	once     sync.Once
}

func (s *eventSub) wants(t string) bool {
	if len(s.prefixes) == 0 {
		return true
	}
	for _, p := range s.prefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// NewBus returns an empty bus.
func NewBus() *Bus {
	return &Bus{next: 1, subs: map[*eventSub]struct{}{}, tokens: map[string]string{}}
}

// Token returns a fresh token for a plugin start; the previous one of that
// plugin stops working.
func (b *Bus) Token(plugin string) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	t := hex.EncodeToString(raw)
	b.mu.Lock()
	for k, v := range b.tokens {
		if v == plugin {
			delete(b.tokens, k)
		}
	}
	b.tokens[t] = plugin
	b.mu.Unlock()
	return t
}

// pluginOf returns the plugin a token belongs to.
func (b *Bus) pluginOf(token string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	name, ok := b.tokens[token]
	return name, ok
}

// Publish puts an event on the bus. data is a JSON object or nil.
func (b *Bus) Publish(source, typ string, data interface{}) (Event, error) {
	if !eventType.MatchString(typ) || len(typ) > 96 {
		return Event{}, fmt.Errorf("event type %q: two to six dot-separated parts of a-z, 0-9, - and _", typ)
	}
	var raw json.RawMessage
	switch d := data.(type) {
	case nil:
	case json.RawMessage:
		raw = d
	default:
		var err error
		if raw, err = json.Marshal(d); err != nil {
			return Event{}, err
		}
	}
	if len(raw) > eventDataMax {
		return Event{}, fmt.Errorf("event data larger than %d KB", eventDataMax>>10)
	}
	if len(raw) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return Event{}, fmt.Errorf("event data must be a JSON object")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := Event{ID: b.next, Time: time.Now().UTC(), Type: typ, Source: source, Data: raw}
	b.next++
	b.kept = append(b.kept, e)
	if len(b.kept) > eventsKept {
		b.kept = append([]Event(nil), b.kept[len(b.kept)-eventsKept:]...)
	}
	for s := range b.subs {
		if !s.wants(typ) {
			continue
		}
		select {
		case s.c <- e:
		default:
			s.once.Do(func() { close(s.lost) })
			delete(b.subs, s)
		}
	}
	return e, nil
}

// Since returns the kept events after id that match the prefixes.
func (b *Bus) Since(id uint64, prefixes []string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &eventSub{prefixes: prefixes}
	out := []Event{}
	for _, e := range b.kept {
		if e.ID > id && s.wants(e.Type) {
			out = append(out, e)
		}
	}
	return out
}

// subscribe registers a subscriber; with catchUp it also returns the kept
// events after id after.
func (b *Bus) subscribe(prefixes []string, after uint64, catchUp bool) (*eventSub, []Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= eventSubs {
		return nil, nil, sdk.NewError(http.StatusServiceUnavailable, "too_many_clients", "too many event streams")
	}
	s := &eventSub{prefixes: prefixes, c: make(chan Event, eventSubBacklog), lost: make(chan struct{})}
	var backlog []Event
	if catchUp {
		for _, e := range b.kept {
			if e.ID > after && s.wants(e.Type) {
				backlog = append(backlog, e)
			}
		}
	}
	b.subs[s] = struct{}{}
	return s, backlog, nil
}

func (b *Bus) unsubscribe(s *eventSub) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

func prefixesOf(r *http.Request) []string {
	var out []string
	for _, v := range r.URL.Query()["type"] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// --- HTTP

// handleEvents: GET api/events?since=<id>&type=<prefix>,… returns the kept
// events after id.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	writeJSON(w, map[string]interface{}{"events": s.Events.Since(since, prefixesOf(r))})
}

// handleEventStream: GET api/events/stream?type=<prefix>,… streams events as
// server-sent events ("event: rcplug", the JSON in data, the id in id); with
// Last-Event-ID (or ?since=) it first sends the kept events after that id.
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	last := r.Header.Get("Last-Event-ID")
	if v := r.URL.Query().Get("since"); v != "" {
		last = v
	}
	after, err := strconv.ParseUint(last, 10, 64)
	sub, backlog, err := s.Events.subscribe(prefixesOf(r), after, err == nil)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	defer s.Events.unsubscribe(sub)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	send := func(e Event) bool {
		raw, _ := json.Marshal(e)
		_, err := fmt.Fprintf(w, "id: %d\nevent: rcplug\ndata: %s\n\n", e.ID, raw)
		return err == nil
	}
	_, _ = fmt.Fprint(w, ": rcplug events\n\n")
	for _, e := range backlog {
		if !send(e) {
			return
		}
	}
	fl.Flush()
	beat := time.NewTicker(25 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.lost:
			return // too slow: the client reconnects with Last-Event-ID
		case e := <-sub.c:
			if !send(e) {
				return
			}
			fl.Flush()
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// handlePublish: POST api/events {"type", "data"} from a plugin, which shows
// its token (X-Rcplug-Token); the host sets the source.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	name, ok := s.Events.pluginOf(r.Header.Get("X-Rcplug-Token"))
	if !ok {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "plugin_token", "only plugins publish events (X-Rcplug-Token, from RCPLUG_TOKEN)"))
		return
	}
	var req struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, eventDataMax+1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "body must be {\"type\": ..., \"data\": {...}}"))
		return
	}
	if strings.HasPrefix(req.Type, "host.") {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "reserved", "event types starting with host. are the host's"))
		return
	}
	if string(req.Data) == "null" {
		req.Data = nil
	}
	e, err := s.Events.Publish(name, req.Type, req.Data)
	if err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_event", err.Error()))
		return
	}
	writeJSON(w, map[string]uint64{"id": e.ID})
}

// hostEvent publishes one of the host's own events (best effort).
func (m *Manager) hostEvent(typ string, data interface{}) {
	if m.Events != nil {
		_, _ = m.Events.Publish("host", typ, data)
	}
}

// watchEvents runs fn for each event of the given prefixes until ctx ends
// (for the host's own use, e.g. tests).
func (b *Bus) watchEvents(ctx context.Context, prefixes []string, fn func(Event)) error {
	sub, _, err := b.subscribe(prefixes, 0, false)
	if err != nil {
		return err
	}
	defer b.unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.lost:
			return nil
		case e := <-sub.c:
			fn(e)
		}
	}
}
