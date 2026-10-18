package host

import (
	"context"
	"os"
	"testing"
	"time"
)

// With a running JACK server (on a computer: jackd -d dummy -r 48000) and
// the ALSA loopback module: RCPLUG_TEST_JACK=1 go test -run JackBridge.
func TestJackBridge(t *testing.T) {
	if os.Getenv("RCPLUG_TEST_JACK") == "" {
		t.Skip("set RCPLUG_TEST_JACK=1 with a running JACK server")
	}
	taps := NewTaps(NewJackBridge())
	ctx := context.Background()
	ports, err := taps.bridge.Ports(ctx)
	if err != nil || len(ports) < 2 {
		t.Fatalf("ports %v %v", ports, err)
	}
	sub, err := taps.Open(ctx, "test", ports[:2])
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	deadline := time.After(5 * time.Second)
	for got < tapRate*2*2/2 { // half a second of two channels
		select {
		case b := <-sub.out:
			got += len(b)
		case <-deadline:
			t.Fatalf("only %d bytes of audio", got)
		}
	}
	st := taps.Status()
	t.Logf("status %+v", st)
	if !st.Running || len(st.Channels) != 2 {
		t.Fatalf("status %+v", st)
	}
	taps.Close(sub)
	taps.Disable()
	if st := taps.Status(); st.Running {
		t.Fatal("still running")
	}
}
