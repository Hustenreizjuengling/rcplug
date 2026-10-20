package host

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// The device's screen for a plugin that takes it over (permission
// "screen"): while such a plugin runs, RØDE's screen program and the loop
// that restarts it (display-start.sh) are stopped; when the plugin ends,
// however it ends, rcplugd starts RØDE's display service again. Nothing of
// RØDE's is changed; the screen program is only not running meanwhile.
//
// A marker file (ScreenMarker) says the screen is away on purpose: the
// console reads it and stays alone with the mixer instead of waiting for the
// screen, and a restarted rcplugd that finds it hands the screen back.

// ScreenMarker is where the marker lives; /run is empty after a restart.
const ScreenMarker = "/run/rcplug-screen"

// Screen hands the device's screen to a plugin and back.
type Screen struct {
	Marker   string
	Service  string // RØDE's display service, started to hand the screen back
	Loop     string // the script that restarts the screen program (its name in the command line)
	App      string // the screen program (its comm)
	procRoot string
	logf     func(string, ...interface{})
	// start runs the display service's start; tests replace it.
	start func(service string) error
	// BootWait is how long a remembered screen plugin waits at rcplugd's
	// start for RØDE's screen program to appear before it takes the screen
	// anyway (rcplugd starts before the display service at boot).
	BootWait time.Duration

	mu     sync.Mutex
	holder string
}

// AppRunning reports whether RØDE's screen program is running.
func (s *Screen) AppRunning() bool {
	return len(s.find(func(comm, _ string) bool { return comm == s.App })) > 0
}

// NewScreen returns the controller for RØDE's screen on the Duo.
func NewScreen(logf func(string, ...interface{})) *Screen {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Screen{Marker: ScreenMarker, Service: "/etc/init.d/rode-display-service", Loop: "display-start.sh",
		App: "rc_gui_juce", procRoot: "/proc", logf: logf, BootWait: 90 * time.Second, start: func(service string) error {
			return spawnDetached(`exec /bin/sh "$1" start >/dev/null 2>&1`, service)
		}}
}

var errScreenTaken = sdk.NewError(http.StatusConflict, "screen_taken", "another plugin has the device's screen")

// Holder names the plugin that has the screen ("" if none).
func (s *Screen) Holder() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holder
}

// Take stops RØDE's screen for plugin name.
func (s *Screen) Take(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holder != "" && s.holder != name {
		return errScreenTaken
	}
	// The marker first: the console must not start the screen again while
	// it goes away.
	if err := os.WriteFile(s.Marker, []byte(name+"\n"), 0o644); err != nil {
		return fmt.Errorf("taking the screen: %w", err)
	}
	s.holder = name
	loops := s.find(func(_ string, cmdline string) bool { return strings.Contains(cmdline, s.Loop) })
	signal(loops, syscall.SIGTERM)
	apps := s.find(func(comm, _ string) bool { return comm == s.App })
	signal(apps, syscall.SIGTERM)
	for end := time.Now().Add(3 * time.Second); len(apps) > 0 && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		apps = s.find(func(comm, _ string) bool { return comm == s.App })
	}
	signal(apps, syscall.SIGKILL)
	s.logf("screen: RØDE's screen stopped for %s", name)
	return nil
}

// Transfer passes the screen from one plugin to another without RØDE's
// screen in between (a handover: the first plugin stops, the second starts).
func (s *Screen) Transfer(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holder != from {
		return errScreenTaken
	}
	if err := os.WriteFile(s.Marker, []byte(to+"\n"), 0o644); err != nil {
		return fmt.Errorf("handing the screen over: %w", err)
	}
	s.holder = to
	s.logf("screen: handed over from %s to %s", from, to)
	return nil
}

// Release starts RØDE's screen again if name has it ("" releases whoever
// has it, or a screen left away by an earlier rcplugd).
func (s *Screen) Release(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name != "" && s.holder != name {
		return
	}
	_, err := os.Stat(s.Marker)
	if s.holder == "" && errors.Is(err, os.ErrNotExist) {
		return
	}
	who := s.holder
	s.holder = ""
	running := len(s.find(func(_ string, cmdline string) bool { return strings.Contains(cmdline, s.Loop) })) > 0 ||
		len(s.find(func(comm, _ string) bool { return comm == s.App })) > 0
	if !running {
		if _, err := os.Stat(s.Service); err == nil {
			if err := s.start(s.Service); err != nil {
				s.logf("screen: starting RØDE's screen: %v", err)
			}
		}
	}
	_ = os.Remove(s.Marker)
	if who == "" {
		who = "an earlier rcplugd"
	}
	s.logf("screen: RØDE's screen back (it was with %s)", who)
}

// find lists processes by their name (comm) and command line.
func (s *Screen) find(match func(comm, cmdline string) bool) []int {
	dirs, _ := filepath.Glob(filepath.Join(s.procRoot, "[0-9]*"))
	var out []int
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil || pid == os.Getpid() {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(d, "comm"))
		if err != nil {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join(d, "cmdline"))
		if match(strings.TrimSpace(string(comm)), strings.ReplaceAll(string(cmd), "\x00", " ")) {
			out = append(out, pid)
		}
	}
	return out
}
