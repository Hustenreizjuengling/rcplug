package rcsim

import (
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

func TestClientGoroutinesEndOnDisconnect(t *testing.T) {
	s, err := NewDuo(Options{Logf: func(string, ...interface{}) {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		c, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1024)
		_, _ = c.Read(buf)
		c.Close()
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base+2 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines leaked: %d (base %d)", runtime.NumGoroutine(), base)
		}
		time.Sleep(20 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	s.Close() // concurrent Close must be safe
	<-done
}

func TestRealisticProfileNeverInventsProperties(t *testing.T) {
	root, err := DuoTree()
	if err != nil {
		t.Fatal(err)
	}
	before, props := root.Count()
	MakeRealistic(root)
	after, props2 := root.Count()
	if before != after || props != props2 {
		t.Fatalf("profile changed the tree shape: %d/%d -> %d/%d", before, props, after, props2)
	}
}

func TestOneClientLikeTheMixer(t *testing.T) {
	s, err := NewDuo(Options{OneClient: true, Ping: 20 * time.Millisecond, Logf: func(string, ...interface{}) {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	fr := rcproto.NewFrameReader(a)
	if body, err := fr.Next(); err != nil || body[0] != byte(rcproto.FullSync) {
		t.Fatalf("first frame: %v", err)
	}
	if body, err := fr.Next(); err != nil || string(body) != "ping" {
		t.Fatalf("expected a ping: %q %v", body, err)
	}
	if s.Listening() {
		t.Fatal("still listening with a client connected")
	}
	if c, err := net.Dial("tcp", s.Addr()); err == nil {
		c.Close()
		t.Fatal("a second client got in")
	}
	a.Close()
	deadline := time.Now().Add(3 * time.Second)
	for !s.Listening() {
		if time.Now().After(deadline) {
			t.Fatal("not listening again after the client left")
		}
		time.Sleep(5 * time.Millisecond)
	}
	b, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	for s.Listening() { // accepted
		time.Sleep(5 * time.Millisecond)
	}
	b.Close()

	// Someone else on the address when the client leaves: the mixer could
	// not listen again.
	for !s.Listening() {
		time.Sleep(5 * time.Millisecond)
	}
	c, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	for s.Listening() {
		time.Sleep(5 * time.Millisecond)
	}
	squat, err := net.Listen("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer squat.Close()
	c.Close()
	for s.RelistenFailures() == 0 {
		if time.Now().After(deadline.Add(3 * time.Second)) {
			t.Fatal("relisten failure not counted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
