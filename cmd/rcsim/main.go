// Command rcsim runs a simulated RØDECaster control port (rc_audio_mixer,
// TCP 2345) for developing and testing the console without hardware.
//
//	rcsim --addr 127.0.0.1:2345            # realistic Duo with meters
//	rcsim --exclusive                      # behave like a single-client desk
//	rcsim --fixture pro2.frame             # serve a tree captured with rccapture
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2345", "listen address")
	fixture := flag.String("fixture", "", "full-sync frame to serve instead of the built-in Duo (from rccapture)")
	hz := flag.Float64("meter-hz", 15, "meter updates per second (0 = off)")
	animate := flag.Bool("animate", true, "slowly move physical fader 1 now and then")
	exclusive := flag.Bool("exclusive", false, "drop existing clients when a new one connects")
	waitHS := flag.Bool("wait-handshake", false, "send the full sync only after a session-open handshake")
	oneClient := flag.Bool("one-client", false, "serve one client like the Duo's mixer: stop listening while a client is connected")
	ping := flag.Duration("ping", 0, "send a ping frame to clients at this interval, like the desk (0 = never)")
	flag.Parse()

	frame := rcsim.DuoFrame
	if *fixture != "" {
		var err error
		if frame, err = os.ReadFile(*fixture); err != nil {
			log.Fatal(err)
		}
	}
	opts := rcsim.Options{MeterHz: *hz, Animate: *animate, Exclusive: *exclusive, WaitHandshake: *waitHS, OneClient: *oneClient, Ping: *ping}
	s, err := rcsim.NewFromFrame(frame, opts)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.Listen(*addr); err != nil {
		log.Fatal(err)
	}
	p := rcproto.ProfileOf(rcproto.DetectModel(s.Snapshot()))
	log.Printf("rcsim: simulated %s on %s", p.Name, s.Addr())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	s.Close()
}
