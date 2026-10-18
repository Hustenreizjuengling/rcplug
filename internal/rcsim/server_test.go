package rcsim

import (
	"net"
	"runtime"
	"testing"
	"time"
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
