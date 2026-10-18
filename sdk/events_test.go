package sdk_test

import (
	"context"
	"net/http/httptest"
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
