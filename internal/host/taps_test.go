package host

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBridge plays a known pattern: channel c of frame f carries
// int16(c*1000 + f%1000).
type fakeBridge struct {
	mu        sync.Mutex
	ports     []string
	connected map[int]string
	starts    int
	stops     int
	running   bool
	widths    []int
	slow      time.Duration // Connect takes this long (jack_connect on a busy device)
	pw        *io.PipeWriter
	failConn  string // Connect of this port fails
	failStart bool   // the next Start fails
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{ports: []string{"system:capture_1", "system:capture_2", "JUCEJack:out_1", "JUCEJack:out_2"}, connected: map[int]string{}}
}

func (f *fakeBridge) Ports(ctx context.Context) ([]string, error) { return f.ports, nil }

func (f *fakeBridge) Start(ctx context.Context, channels int) (io.ReadCloser, error) {
	f.mu.Lock()
	if f.failStart {
		f.failStart = false
		f.mu.Unlock()
		return nil, errors.New("alsa_out failed")
	}
	f.mu.Unlock()
	pr, pw := io.Pipe()
	f.mu.Lock()
	f.starts++
	f.widths = append(f.widths, channels)
	f.running = true
	f.pw = pw
	f.mu.Unlock()
	go func() {
		frame := 0
		size := channels * 2
		buf := make([]byte, tapChunk*size)
		for {
			for i := 0; i < tapChunk; i++ {
				for c := 0; c < channels; c++ {
					binary.LittleEndian.PutUint16(buf[i*size+c*2:], uint16(int16(c*1000+frame%1000)))
				}
				frame++
			}
			if _, err := pw.Write(buf); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return pr, nil
}

func (f *fakeBridge) Connect(port string, ch int) error {
	f.mu.Lock()
	slow := f.slow
	f.mu.Unlock()
	time.Sleep(slow)
	f.mu.Lock()
	defer f.mu.Unlock()
	if port == f.failConn {
		return errors.New("jack_connect failed")
	}
	f.connected[ch] = port
	return nil
}

func (f *fakeBridge) Disconnect(port string, ch int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connected[ch] != port {
		return errors.New("not connected")
	}
	delete(f.connected, ch)
	return nil
}

func (f *fakeBridge) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.running = false
	if f.pw != nil {
		f.pw.Close()
	}
}

func (f *fakeBridge) Problems() int { return 0 }

func (f *fakeBridge) state() (starts, stops int, connected map[int]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := map[int]string{}
	for k, v := range f.connected {
		c[k] = v
	}
	return f.starts, f.stops, c
}

func TestTapsShareOneBridge(t *testing.T) {
	old := tapIdleStop
	tapIdleStop = 100 * time.Millisecond
	t.Cleanup(func() { tapIdleStop = old })
	fb := newFakeBridge()
	taps := NewTaps(fb)
	ctx := context.Background()

	if _, err := taps.Open(ctx, "sonar", []string{"nope:out"}); err == nil {
		t.Fatal("an unknown port was tapped")
	}
	a, err := taps.Open(ctx, "sonar", []string{"system:capture_1", "system:capture_2"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := taps.Open(ctx, "meter", []string{"system:capture_2", "JUCEJack:out_1"})
	if err != nil {
		t.Fatal(err)
	}
	// Two ports fit the first bridge of two channels; the third makes it
	// restart with four, and the first stream goes on.
	starts, _, conn := fb.state()
	fb.mu.Lock()
	widths := append([]int(nil), fb.widths...)
	fb.mu.Unlock()
	if starts != 2 || len(conn) != 3 || len(widths) != 2 || widths[0] != 2 || widths[1] != 4 {
		t.Fatalf("a bridge of 2, then 4 channels expected: starts %d, widths %v, connected %v", starts, widths, conn)
	}
	for len(a.out) > 0 { // chunks from before the restart
		<-a.out
	}
	// Each stream gets its ports, in its order: sonar channels 1,2; meter 2,3.
	check := func(s *tapSub, want []int) {
		t.Helper()
		select {
		case chunk := <-s.out:
			n := len(want)
			if len(chunk)%(n*2) != 0 {
				t.Fatalf("chunk of %d bytes for %d channels", len(chunk), n)
			}
			for i, c := range want {
				v := int16(binary.LittleEndian.Uint16(chunk[i*2:]))
				if v/1000 != int16(c) {
					t.Fatalf("stream channel %d carries %d, want bridge channel %d", i, v, c)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no audio")
		}
	}
	check(a, []int{0, 1})
	check(b, []int{1, 2})
	st := taps.Status()
	if !st.Running || st.Width != 4 || len(st.Streams) != 2 || len(st.Channels) != 3 || st.Channels[1].Streams != 2 {
		t.Fatalf("status %+v", st)
	}

	taps.Close(a)
	if _, _, conn := fb.state(); len(conn) != 2 || conn[1] != "" && conn[1] == "system:capture_1" {
		t.Fatalf("after one stream: %v", conn)
	}
	taps.Close(b)
	waitFor(t, "the idle bridge stops", 3*time.Second, func() bool { _, stops, _ := fb.state(); return stops == 2 }) // one was the restart
	if st := taps.Status(); st.Running || len(st.Channels) != 0 {
		t.Fatalf("after stop %+v", st)
	}
}

func TestTapStreamOverHTTP(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "sonar", "#!/bin/sh\n", `{"exec":["./run.sh"],"permissions":["audio"]}`)
	writePlugin(t, root, "quiet", "#!/bin/sh\n", `{"exec":["./run.sh"]}`)
	m := NewManager(root, 19900)
	m.Events = NewBus()
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	srv := NewServer(m, nil, "test")
	srv.Events = m.Events
	fb := newFakeBridge()
	srv.Taps = NewTaps(fb)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	get := func(token string) *http.Response {
		req, _ := http.NewRequest("GET", hs.URL+"/api/audio/stream?ports=JUCEJack:out_1,JUCEJack:out_2", nil)
		if token != "" {
			req.Header.Set("X-Rcplug-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := get(""); r.StatusCode != 403 {
		t.Fatalf("without a token: %d", r.StatusCode)
	}
	token := m.Events.Token("sonar")
	if r := get(m.Events.Token("quiet")); r.StatusCode != 403 {
		t.Fatalf("a plugin without the audio permission: %d", r.StatusCode)
	}
	resp := get(token)
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Audio-Channels") != "2" || resp.Header.Get("X-Audio-Rate") != "48000" {
		t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
	}
	buf := make([]byte, 4*100)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	if v := int16(binary.LittleEndian.Uint16(buf[2:])); v/1000 != 1 {
		t.Fatalf("second channel carries %d", v)
	}
	res, _ := http.Get(hs.URL + "/api/audio")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"plugin":"sonar"`) || !strings.Contains(string(body), `"running":true`) {
		t.Fatalf("status %s", body)
	}
}

// Opening a stream runs JACK's tools under the taps' lock; the streams that
// run meanwhile go on receiving audio (before, the bridge's buffer overran).
func TestTapsReadOnWhileConnecting(t *testing.T) {
	fb := newFakeBridge()
	taps := NewTaps(fb)
	a, err := taps.Open(context.Background(), "sonar", []string{"system:capture_1"})
	if err != nil {
		t.Fatal(err)
	}
	fb.mu.Lock()
	fb.slow = 400 * time.Millisecond
	fb.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := taps.Open(context.Background(), "meter", []string{"JUCEJack:out_1"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // the second Open is in jack_connect now
	got := 0
	deadline := time.After(300 * time.Millisecond)
	for got < 5 {
		select {
		case <-a.out:
			got++
		case <-deadline:
			t.Fatalf("only %d chunks while another stream was connecting", got)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	taps.Disable()
}

// A failed request leaves no bridge running for nobody, and a failed restart
// ends the streams and forgets their channels.
func TestTapsCleanUpAfterFailures(t *testing.T) {
	old := tapIdleStop
	tapIdleStop = 50 * time.Millisecond
	t.Cleanup(func() { tapIdleStop = old })
	fb := newFakeBridge()
	fb.failConn = "JUCEJack:out_1"
	taps := NewTaps(fb)
	if _, err := taps.Open(context.Background(), "p", []string{"JUCEJack:out_1"}); err == nil {
		t.Fatal("open succeeded")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		fb.mu.Lock()
		running := fb.running
		fb.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bridge runs on for nobody after a failed open")
		}
		time.Sleep(10 * time.Millisecond)
	}

	fb.failConn = ""
	sub, err := taps.Open(context.Background(), "p", []string{"system:capture_1"})
	if err != nil {
		t.Fatal(err)
	}
	fb.mu.Lock()
	fb.failStart = true // the restart for a wider bridge fails
	fb.mu.Unlock()
	if _, err := taps.Open(context.Background(), "q", []string{"system:capture_2", "JUCEJack:out_1", "JUCEJack:out_2"}); err == nil {
		t.Fatal("widening succeeded")
	}
	select {
	case <-sub.done:
	case <-time.After(time.Second):
		t.Fatal("the first stream hangs after the failed restart")
	}
	taps.mu.Lock()
	stale := taps.ports[0] != "" || taps.refs[0] != 0
	taps.mu.Unlock()
	if stale {
		t.Fatal("the channel table kept a port of the stopped bridge")
	}
	// The next stream works again from scratch.
	sub2, err := taps.Open(context.Background(), "p", []string{"system:capture_1"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, conn := fb.state()
	if conn[1] != "system:capture_1" {
		t.Fatalf("not wired again: %v", conn)
	}
	taps.Close(sub2)
}
