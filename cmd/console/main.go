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
	"os"
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
		untested  = flag.Bool("allow-untested-model", false, "offer control on models without a verified profile (e.g. Pro II)")
		screenSrc = flag.String("screen", "auto", "screen mirror source: auto (DRM first, then the framebuffer) | fb | drm | off (users must still enable it in the UI)")
		fbDev     = flag.String("fb", "/dev/fb0", "framebuffer device for the screen mirror")
		drmDev    = flag.String("drm", "/dev/dri/card0", "DRM card for the screen mirror when the GUI bypasses the framebuffer")
		fps       = flag.Float64("fps", 2, "max screen mirror frames per second")
		panel     = flag.Bool("panel", false, "EXPERIMENTAL: allow emulating front-panel buttons via TIOCSTI (expert level only)")
		tty       = flag.String("tty", "/dev/ttyAML3", "front-panel serial tty")
		sim       = flag.Bool("sim", false, "start a built-in simulated desk and connect to it (demo without hardware)")
		legacyPIN = flag.String("pin", os.Getenv("RCPLUG_CONSOLE_PIN"), "deprecated: taken over once as the console PIN if none is set; set the PIN in the UI instead")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
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

	client := desk.NewClient(desk.Config{
		Addr: *deskAddr, Guard: *guard, BootGrace: *bootGrace, ReadOnly: *readOnly, Handshake: *handshake,
	}, desk.NewMirror())
	// Started by rcplugd, settings and the PIN live in the plugin's data
	// directory. Run standalone (e.g. go run ./cmd/console --sim) they stay in
	// memory, so nothing lands in the source tree.
	dataDir := os.Getenv("RCPLUG_DATA_DIR")
	if dataDir == "" {
		log.Printf("console: RCPLUG_DATA_DIR not set, settings and PIN are kept in memory only")
	}
	opts := console.Options{
		Client: client, DataDir: dataDir, AllowUntested: *untested, LegacyPIN: *legacyPIN, Version: version,
		Panel: &hw.Panel{TTY: *tty}, PanelEnabled: *panel, Logf: log.Printf,
	}
	if *screenSrc != "off" {
		opts.Screen = &hw.Screen{FB: *fbDev, DRM: *drmDev, Mode: *screenSrc, MaxFPS: *fps}
	}
	c := console.New(opts)
	go client.Run(ctx)
	go c.Run(ctx)

	log.Printf("console %s on %s, desk %s (guard=%v read-only=%v panel=%v)", version, *addr, *deskAddr, *guard, *readOnly, *panel)
	if err := sdk.Serve(ctx, *addr, c.Handler()); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
