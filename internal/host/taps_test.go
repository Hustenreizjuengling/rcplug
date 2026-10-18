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
	pw        *io.PipeWriter
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{ports: []string{"system:capture_1", "system:capture_2", "JUCEJack:out_1", "JUCEJack:out_2"}, connected: map[int]string{}}
}

func (f *fakeBridge) Ports(ctx context.Context) ([]string, error) { return f.ports, nil }

func (f *fakeBridge) Start(ctx context.Context) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	f.mu.Lock()
	f.starts++
	f.running = true
	f.pw = pw
	f.mu.Unlock()
	go func() {
		frame := 0
		buf := make([]byte, tapChunk*tapFrame)
		for {
			for i := 0; i < tapChunk; i++ {
				for c := 0; c < tapChannels; c++ {
					binary.LittleEndian.PutUint16(buf[i*tapFrame+c*2:], uint16(int16(c*1000+frame%1000)))
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
	defer f.mu.Unlock()
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
	on := false
	var mu sync.Mutex
	taps := NewTaps(fb, func() bool { mu.Lock(); defer mu.Unlock(); return on })
	ctx := context.Background()

	if _, err := taps.Open(ctx, "sonar", []string{"system:capture_1"}); err != errTapsOff {
		t.Fatalf("switched off: %v", err)
	}
	mu.Lock()
	on = true
	mu.Unlock()
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
	starts, _, conn := fb.state()
	if starts != 1 || len(conn) != 3 {
		t.Fatalf("one bridge with three channels expected: starts %d, connected %v", starts, conn)
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
	if !st.Running || len(st.Streams) != 2 || len(st.Channels) != 3 || st.Channels[1].Streams != 2 {
		t.Fatalf("status %+v", st)
	}

	taps.Close(a)
	if _, _, conn := fb.state(); len(conn) != 2 || conn[1] != "" && conn[1] == "system:capture_1" {
		t.Fatalf("after one stream: %v", conn)
	}
	taps.Close(b)
	waitFor(t, "the idle bridge stops", 3*time.Second, func() bool { _, stops, _ := fb.state(); return stops == 1 })
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
	srv.Taps = NewTaps(fb, m.AudioTaps)
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
	if r := get(token); r.StatusCode != 403 {
		t.Fatalf("switched off: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("POST", hs.URL+"/api/audio/enable?on=true", nil)
	req.Header.Set("X-Rcplug", "1")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("enable: %v", err)
	}
	if !m.AudioTaps() {
		t.Fatal("the switch was not kept")
	}
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
