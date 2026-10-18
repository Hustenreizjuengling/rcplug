package host

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Shared audio taps: on the Duo the whole desk is a JACK graph, and a plugin
// that wants to hear an input or an output (Sonar, a loudness meter) needs a
// client in it. Instead of one per plugin, the host runs one bridge for all
// of them, "rcplug-taps" with a few channels, and hands each plugin the ports
// it asks for as a stream. One switch for all of it (off by default, as it
// adds a client to RØDE's graph), one place to see what runs and whether
// audio drops out. Only plugins get audio (their token); the network does
// not.

const (
	tapChannels  = 8
	tapRate      = 48000
	tapFrame     = tapChannels * 2 // S16_LE
	tapChunk     = 960             // frames per read: 20 ms
	tapSubBuffer = 50              // chunks (one second) queued per stream
)

// tapIdleStop: how long the bridge runs on without a stream (tests shorten it).
var tapIdleStop = 10 * time.Second

// tapBridge is the JACK bridge (jackBridge on the device; tests use a fake).
type tapBridge interface {
	// Ports lists JACK's output ports (what can be tapped).
	Ports(ctx context.Context) ([]string, error)
	// Start runs the bridge; the reader delivers frames of tapChannels
	// interleaved S16_LE samples at tapRate.
	Start(ctx context.Context) (io.ReadCloser, error)
	// Connect and Disconnect wire a port to one of the bridge's channels
	// (1-based).
	Connect(port string, channel int) error
	Disconnect(port string, channel int) error
	// Stop ends the bridge (its connections go with it).
	Stop()
	// Problems counts dropouts the bridge reported.
	Problems() int
}

// Taps hands out JACK ports to plugins through one bridge.
type Taps struct {
	bridge  tapBridge
	enabled func() bool

	mu       sync.Mutex
	running  bool
	reader   io.ReadCloser
	stopRead context.CancelFunc
	ports    [tapChannels]string // port per channel, "" = free
	refs     [tapChannels]int
	subs     map[*tapSub]struct{}
	idle     *time.Timer
	since    time.Time
	errMsg   string
}

type tapSub struct {
	plugin   string
	ports    []string
	channels []int // 0-based
	out      chan []byte
	done     chan struct{}
	once     sync.Once
	since    time.Time
}

func (s *tapSub) close() { s.once.Do(func() { close(s.done) }) }

// NewTaps makes the taps; enabled reports the user's opt-in.
func NewTaps(bridge tapBridge, enabled func() bool) *Taps {
	return &Taps{bridge: bridge, enabled: enabled, subs: map[*tapSub]struct{}{}}
}

var (
	errTapsOff  = sdk.NewError(http.StatusForbidden, "audio_taps_off", "shared audio taps are switched off (dashboard → System → Audio taps)")
	errTapsFull = sdk.NewError(http.StatusServiceUnavailable, "audio_taps_full", "every channel of the audio bridge is in use")
)

// Open starts streaming ports to a plugin, starting the bridge if needed.
func (t *Taps) Open(ctx context.Context, plugin string, ports []string) (*tapSub, error) {
	if !t.enabled() {
		return nil, errTapsOff
	}
	if len(ports) == 0 || len(ports) > tapChannels {
		return nil, sdk.NewError(http.StatusBadRequest, "bad_ports", fmt.Sprintf("ask for 1 to %d ports", tapChannels))
	}
	known, err := t.bridge.Ports(ctx)
	if err != nil {
		return nil, sdk.NewError(http.StatusServiceUnavailable, "no_jack", err.Error())
	}
	for _, p := range ports {
		if !contains(known, p) {
			return nil, sdk.NewError(http.StatusBadRequest, "bad_ports", "JACK has no output port "+p)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.idle != nil {
		t.idle.Stop()
		t.idle = nil
	}
	if !t.running {
		if err := t.startLocked(); err != nil {
			return nil, err
		}
	}
	sub := &tapSub{plugin: plugin, ports: ports, out: make(chan []byte, tapSubBuffer), done: make(chan struct{}), since: time.Now()}
	var added []int
	for _, p := range ports {
		ch := -1
		for i, q := range t.ports {
			if q == p {
				ch = i
				break
			}
		}
		if ch < 0 {
			for i, q := range t.ports {
				if q == "" {
					ch = i
					break
				}
			}
			if ch < 0 {
				t.releaseLocked(added)
				return nil, errTapsFull
			}
			if err := t.bridge.Connect(p, ch+1); err != nil {
				t.releaseLocked(added)
				return nil, sdk.NewError(http.StatusServiceUnavailable, "jack_connect", err.Error())
			}
			t.ports[ch] = p
		}
		t.refs[ch]++
		added = append(added, ch)
	}
	sub.channels = added
	t.subs[sub] = struct{}{}
	return sub, nil
}

func (t *Taps) startLocked() error {
	ctx, cancel := context.WithCancel(context.Background())
	r, err := t.bridge.Start(ctx)
	if err != nil {
		cancel()
		t.errMsg = err.Error()
		return sdk.NewError(http.StatusServiceUnavailable, "audio_bridge", err.Error())
	}
	t.running, t.reader, t.stopRead, t.since, t.errMsg = true, r, cancel, time.Now(), ""
	go t.pump(r)
	return nil
}

// releaseLocked drops one reference to each channel and disconnects the
// ports nobody listens to any more.
func (t *Taps) releaseLocked(channels []int) {
	for _, ch := range channels {
		t.refs[ch]--
		if t.refs[ch] <= 0 {
			_ = t.bridge.Disconnect(t.ports[ch], ch+1)
			t.refs[ch], t.ports[ch] = 0, ""
		}
	}
}

// Close ends a stream; the bridge stops a little later if nobody else
// listens (a plugin that reconnects at once keeps it running).
func (t *Taps) Close(sub *tapSub) {
	sub.close()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.subs[sub]; !ok {
		return
	}
	delete(t.subs, sub)
	t.releaseLocked(sub.channels)
	if len(t.subs) == 0 && t.running && t.idle == nil {
		t.idle = time.AfterFunc(tapIdleStop, t.stopIfIdle)
	}
}

func (t *Taps) stopIfIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.idle = nil
	if len(t.subs) == 0 {
		t.stopLocked()
	}
}

func (t *Taps) stopLocked() {
	if !t.running {
		return
	}
	t.running = false
	t.stopRead()
	_ = t.reader.Close()
	t.bridge.Stop()
	for i := range t.ports {
		t.ports[i], t.refs[i] = "", 0
	}
	for s := range t.subs {
		s.close()
	}
	t.subs = map[*tapSub]struct{}{}
}

// Disable stops everything (the user switched the taps off).
func (t *Taps) Disable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

// pump reads the bridge and hands each stream its channels; a stream that
// does not keep up is ended rather than ever holding up the bridge.
func (t *Taps) pump(r io.Reader) {
	buf := make([]byte, tapChunk*tapFrame)
	for {
		n, err := io.ReadFull(r, buf)
		if n >= tapFrame {
			frames := n / tapFrame
			t.mu.Lock()
			for s := range t.subs {
				out := make([]byte, frames*len(s.channels)*2)
				o := 0
				for f := 0; f < frames; f++ {
					base := f * tapFrame
					for _, ch := range s.channels {
						copy(out[o:o+2], buf[base+ch*2:base+ch*2+2])
						o += 2
					}
				}
				select {
				case s.out <- out:
				default:
					s.close()
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			t.mu.Lock()
			if t.running && t.reader == r {
				t.errMsg = "the audio bridge stopped: " + err.Error()
				t.stopLocked()
			}
			t.mu.Unlock()
			return
		}
	}
}

// TapStatus describes the taps for the dashboard.
type TapStatus struct {
	Enabled  bool         `json:"enabled"`
	Running  bool         `json:"running"`
	Since    string       `json:"since,omitempty"`
	Channels []TapChannel `json:"channels"`
	Streams  []TapStream  `json:"streams"`
	Problems int          `json:"problems"`
	Error    string       `json:"error,omitempty"`
}

// TapChannel is a channel of the bridge in use.
type TapChannel struct {
	Channel int    `json:"channel"`
	Port    string `json:"port"`
	Streams int    `json:"streams"`
}

// TapStream is one plugin's stream.
type TapStream struct {
	Plugin string   `json:"plugin"`
	Ports  []string `json:"ports"`
	Since  string   `json:"since"`
}

// Status reports the taps.
func (t *Taps) Status() TapStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := TapStatus{Enabled: t.enabled(), Running: t.running, Channels: []TapChannel{}, Streams: []TapStream{}, Error: t.errMsg}
	if t.running {
		st.Since = t.since.Format(time.RFC3339)
		st.Problems = t.bridge.Problems()
	}
	for i, p := range t.ports {
		if p != "" {
			st.Channels = append(st.Channels, TapChannel{Channel: i + 1, Port: p, Streams: t.refs[i]})
		}
	}
	for s := range t.subs {
		st.Streams = append(st.Streams, TapStream{Plugin: s.plugin, Ports: s.ports, Since: s.since.Format(time.RFC3339)})
	}
	sort.Slice(st.Streams, func(i, j int) bool { return st.Streams[i].Plugin < st.Streams[j].Plugin })
	return st
}

// --- HTTP

func (s *Server) handleTaps(w http.ResponseWriter, r *http.Request) {
	st := s.Taps.Status()
	doc := map[string]interface{}{"status": st}
	if st.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if ports, err := s.Taps.bridge.Ports(ctx); err == nil {
			doc["ports"] = ports
		} else {
			doc["portsError"] = err.Error()
		}
	}
	writeJSON(w, doc)
}

func (s *Server) handleTapsEnable(w http.ResponseWriter, r *http.Request) {
	on, err := strconv.ParseBool(r.URL.Query().Get("on"))
	if err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "on must be true or false"))
		return
	}
	s.m.SetAudioTaps(on)
	if !on {
		s.Taps.Disable()
	}
	s.m.hostEvent("host.audio.taps", map[string]bool{"enabled": on})
	writeJSON(w, s.Taps.Status())
}

// handleTapStream: GET api/audio/stream?ports=a,b from a plugin (its token)
// streams those ports as interleaved signed 16-bit little-endian samples at
// 48 kHz until the plugin hangs up.
func (s *Server) handleTapStream(w http.ResponseWriter, r *http.Request) {
	plugin := ""
	if s.Events != nil {
		plugin, _ = s.Events.pluginOf(r.Header.Get("X-Rcplug-Token"))
	}
	if plugin == "" {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "plugin_token", "only plugins get audio (X-Rcplug-Token, from RCPLUG_TOKEN)"))
		return
	}
	var ports []string
	for _, p := range strings.Split(r.URL.Query().Get("ports"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			ports = append(ports, p)
		}
	}
	sub, err := s.Taps.Open(r.Context(), plugin, ports)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	defer s.Taps.Close(sub)
	fl, _ := w.(http.Flusher)
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("X-Audio-Rate", strconv.Itoa(tapRate))
	h.Set("X-Audio-Channels", strconv.Itoa(len(ports)))
	h.Set("X-Audio-Format", "s16le")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if fl != nil {
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case b := <-sub.out:
			if _, err := w.Write(b); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

// --- the persisted switch

// AudioTaps reports whether the user switched the shared audio taps on.
func (m *Manager) AudioTaps() bool { return m.loadState().AudioTaps }

// SetAudioTaps records the user's choice for the shared audio taps.
func (m *Manager) SetAudioTaps(on bool) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	ps := m.loadState()
	ps.AudioTaps = on
	_ = sdk.SaveJSON(m.stateFile(), ps)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
