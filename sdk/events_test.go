package sdk_test

import (
	"context"
	"encoding/binary"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/host"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

func TestPublishAndEvents(t *testing.T) {
	m := host.NewManager(t.TempDir(), 19700)
	m.Events = host.NewBus()
	srv := host.NewServer(m, nil, "test")
	srv.Events = m.Events
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	t.Setenv("RCPLUG_HOST_URL", hs.URL)

	t.Setenv("RCPLUG_TOKEN", "")
	if err := sdk.Publish(context.Background(), "test.hello", nil); err != sdk.ErrStandalone {
		t.Fatalf("standalone: %v", err)
	}
	t.Setenv("RCPLUG_TOKEN", m.Events.Token("tester"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := sdk.Events(ctx, "test.")
	time.Sleep(200 * time.Millisecond) // the stream is up
	if err := sdk.Publish(ctx, "other.skipped", nil); err != nil {
		t.Fatal(err)
	}
	if err := sdk.Publish(ctx, "test.hello", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := sdk.Publish(ctx, "bad type", nil); err == nil {
		t.Fatal("a bad type was published")
	}
	select {
	case e := <-events:
		if e.Type != "test.hello" || e.Source != "tester" || string(e.Data) != `{"n":1}` {
			t.Fatalf("got %+v", e)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
}

// toneBridge stands in for the JACK bridge: every sample of channel c is c.
type toneBridge struct{ pw *io.PipeWriter }

func (b *toneBridge) Ports(ctx context.Context) ([]string, error) {
	return []string{"system:capture_1", "system:capture_2"}, nil
}

func (b *toneBridge) Start(ctx context.Context) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	b.pw = pw
	go func() {
		frame := make([]byte, 8*2*960)
		for i := 0; i < len(frame); i += 2 {
			binary.LittleEndian.PutUint16(frame[i:], uint16(i/2%8))
		}
		for {
			if _, err := pw.Write(frame); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return pr, nil
}
func (b *toneBridge) Connect(string, int) error    { return nil }
func (b *toneBridge) Disconnect(string, int) error { return nil }
func (b *toneBridge) Stop()                        { b.pw.Close() }
func (b *toneBridge) Problems() int                { return 0 }

func TestAudioTap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "meter")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"exec":["true"],"permissions":["audio"]}`), 0o644)
	m := host.NewManager(root, 19750)
	m.Events = host.NewBus()
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	m.SetAudioTaps(true)
	srv := host.NewServer(m, nil, "test")
	srv.Events = m.Events
	srv.Taps = host.NewTaps(&toneBridge{}, m.AudioTaps)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	t.Setenv("RCPLUG_HOST_URL", hs.URL)
	t.Setenv("RCPLUG_TOKEN", m.Events.Token("meter"))
	ctx := context.Background()
	on, ports, err := sdk.AudioPorts(ctx)
	if err != nil || !on || len(ports) != 2 {
		t.Fatalf("ports %v %v %v", on, ports, err)
	}
	r, err := sdk.AudioTap(ctx, "system:capture_2")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, 64)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	// The port took the bridge's first free channel (JACK routes it there);
	// the stream has that one channel only.
	for i := 0; i < len(buf); i += 2 {
		if v := binary.LittleEndian.Uint16(buf[i:]); v != 0 {
			t.Fatalf("sample %d at %d", v, i)
		}
	}
	if _, err := sdk.AudioTap(ctx, "nope:port"); err == nil {
		t.Fatal("an unknown port was tapped")
	}
}
