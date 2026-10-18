// Command console is the rcplug "console" plugin: a complete, live mirror of
// the RØDECaster in the browser, with opt-in remote control.
//
// It connects to rc_audio_mixer's control port (TCP 2345 on the device),
// keeps the whole JUCE ValueTree in sync and serves a web UI plus a JSON/SSE
// API (docs/console-api.md). Writes are off by default and guarded by a
// policy (internal/console/policy.go).
//
//	console                       # as an rcplug plugin (address from RCPLUG_PORT)
//	console --sim --addr :8092    # demo against the built-in simulated desk
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/console"
	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/hw"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

var version = "dev"

func main() {
	var (
		addr      = flag.String("addr", sdk.ListenAddr("127.0.0.1:8082"), "HTTP listen address")
		deskAddr  = flag.String("mixer", envOr("RCPLUG_MIXER", "127.0.0.1:2345"), "rc_audio_mixer control port")
		guard     = flag.Bool("guard", true, "protect the device GUI's connection (back off if joining displaces a client)")
		bootGrace = flag.Duration("boot-grace", 75*time.Second, "wait until the system is up this long before connecting")
		readOnly  = flag.Bool("read-only", false, "hard-disable all writes (overrides the UI setting)")
		handshake = flag.String("handshake", "auto", "session-open handshake: auto (only if the desk stays silent) | always | never")
		deskMode  = flag.String("desk-mode", "auto", "how to get the desk state: auto (read along with the device's screen on a RØDECaster, whose mixer serves one client; connect elsewhere) | tap | connect")
		untested  = flag.Bool("allow-untested-model", false, "offer control on models without a verified profile (e.g. Pro II)")
		panel     = flag.Bool("panel", false, "EXPERIMENTAL: allow emulating front-panel buttons via TIOCSTI (expert level only)")
		meters    = flag.String("meters", "auto", "level meters and hardware fader positions from the mixer's shared memory (read-only): auto | off")
		tty       = flag.String("tty", "/dev/ttyAML3", "front-panel serial tty")
		sim       = flag.Bool("sim", false, "start a built-in simulated desk and connect to it (demo without hardware)")
		legacyPIN = flag.String("pin", os.Getenv("RCPLUG_CONSOLE_PIN"), "deprecated: taken over once as the console PIN if none is set; set the PIN in the UI instead")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	// The screen mirror and operating the screen from it are gone (0.6.7);
	// their flags are still accepted, so an old configuration starts.
	for _, name := range []string{"screen", "fb", "drm", "fps", "touch"} {
		flag.String(name, "", "no longer used")
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	for _, f := range []struct{ name, value, allowed string }{
		{"handshake", *handshake, "auto always never"},
		{"meters", *meters, "auto off"},
	} {
		if !strings.Contains(" "+f.allowed+" ", " "+f.value+" ") {
			fmt.Fprintf(os.Stderr, "console: --%s must be one of %s, not %q\n", f.name, strings.ReplaceAll(f.allowed, " ", ", "), f.value)
			os.Exit(2)
		}
	}
	_ = sdk.LowPriority() // no-op when started by rcplugd (already nice 19)
	log.SetFlags(log.LstdFlags)
	ctx, stop := sdk.SignalContext()
	defer stop()

	if *sim {
		s, err := rcsim.NewDuo(rcsim.Options{MeterHz: 15, Animate: true, Logf: func(string, ...interface{}) {}})
		if err != nil {
			log.Fatal(err)
		}
		if err := s.Listen("127.0.0.1:0"); err != nil {
			log.Fatal(err)
		}
		defer s.Close()
		*deskAddr, *bootGrace = s.Addr(), time.Nanosecond
		log.Printf("console: simulated desk on %s", s.Addr())
	}

	// Started by rcplugd, settings and the PIN live in the plugin's data
	// directory. Run standalone (e.g. go run ./cmd/console --sim) they stay in
	// memory, so nothing lands in the source tree.
	dataDir := os.Getenv("RCPLUG_DATA_DIR")
	resume := ""
	if dataDir == "" {
		log.Printf("console: RCPLUG_DATA_DIR not set, settings and PIN are kept in memory only")
	} else if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("console: data directory: %v", err)
	} else {
		resume = filepath.Join(dataDir, "tap-resume")
	}
	client := desk.NewClient(desk.Config{
		Addr: *deskAddr, Guard: *guard, BootGrace: *bootGrace, ReadOnly: *readOnly, Handshake: *handshake, Mode: *deskMode,
		ResumeFile: resume,
	}, desk.NewMirror())
	opts := console.Options{
		Client: client, DataDir: dataDir, AllowUntested: *untested, LegacyPIN: *legacyPIN, Version: version,
		Panel: &hw.Panel{TTY: *tty}, PanelEnabled: *panel, Logf: log.Printf,
	}

	if *meters != "off" {
		m := &hw.Meters{}
		defer m.Close()
		opts.Meters = m
	}

	c := console.New(opts)
	clientDone := make(chan struct{})
	go func() { client.Run(ctx); close(clientDone) }()
	go c.Run(ctx)

	log.Printf("console %s on %s, desk %s (guard=%v read-only=%v panel=%v)", version, *addr, *deskAddr, *guard, *readOnly, *panel)
	h := c.Handler()
	if sdk.Name() == "" {
		h = localHostsOnly(h) // run alone, without rcplugd's checks in front
	}
	if err := sdk.Serve(ctx, *addr, h); err != nil {
		log.Fatal(err)
	}
	// Reading along keeps the state for the next start (an update, a
	// restart); give it a moment.
	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// localHostsOnly answers only requests addressed to an IP address or
// localhost, as rcplugd does for everything behind it. Run alone (during
// development), the console would otherwise be reachable from any web page
// through DNS rebinding.
func localHostsOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && net.ParseIP(host) == nil {
			http.Error(w, "unknown host name "+r.Host+"; open the console by IP address", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
