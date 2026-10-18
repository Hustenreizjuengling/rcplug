package console

import (
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/hw"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Settings are the user's persisted console choices (settings.json in the
// plugin's data directory, kept across reinstalls).
type Settings struct {
	Control string `json:"control"` // off | basic | expert
	// Screen enables the display mirror. Off by default: the mirror shows
	// everything on the device screen, including secrets being typed there.
	Screen bool `json:"screen"`
	// ScreenRotate turns the mirrored picture: "" (auto: a portrait buffer,
	// as on the Duo, becomes landscape), "0", "90", "180" or "270" degrees
	// clockwise.
	ScreenRotate string `json:"screenRotate,omitempty"`
	// Panel switches the front-panel emulation on (--panel does it for good).
	Panel bool `json:"panel,omitempty"`
	// ScreenControl lets clicks on the screen mirror tap the device's
	// touchscreen; TouchFlip corrects its orientation ("", "x", "y", "xy").
	ScreenControl bool   `json:"screenControl,omitempty"`
	TouchFlip     string `json:"touchFlip,omitempty"`
}

// rotations are the valid ScreenRotate values besides "" (auto).
var rotations = map[string]int{"0": 0, "90": 90, "180": 180, "270": 270}

// screenRotation turns a ScreenRotate setting into degrees or hw.RotateAuto.
func screenRotation(s string) int {
	if deg, ok := rotations[s]; ok {
		return deg
	}
	return hw.RotateAuto
}

// pinStore keeps the optional write PIN in a root-only file (never on a
// command line, where the host's plugin log and /proc would expose it) and
// throttles guessing.
type pinStore struct {
	path string

	mu       sync.Mutex
	pin      string
	failures []time.Time
	locked   time.Time
}

const (
	pinMaxFailures = 5
	pinWindow      = 5 * time.Minute
	pinLockout     = time.Minute
)

// newPinStore loads <dir>/pin; with dir == "" the PIN lives in memory only.
func newPinStore(dir string) *pinStore {
	if dir == "" {
		return &pinStore{}
	}
	p := &pinStore{path: filepath.Join(dir, "pin")}
	if raw, err := os.ReadFile(p.path); err == nil {
		p.pin = strings.TrimSpace(string(raw))
	}
	return p
}

func (p *pinStore) set() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pin != ""
}

var (
	errPin       = sdk.NewError(http.StatusUnauthorized, "pin_required", "PIN required or wrong")
	errPinLocked = sdk.NewError(http.StatusTooManyRequests, "pin_locked", "too many wrong PINs; try again in a minute")
)

// check verifies the X-Rcplug-Pin header (no PIN configured = always ok).
func (p *pinStore) check(r *http.Request) error {
	return p.verify(r.Header.Get("X-Rcplug-Pin"))
}

func (p *pinStore) verify(got string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pin == "" {
		return nil
	}
	now := time.Now()
	if now.Before(p.locked) {
		return errPinLocked
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(p.pin)) == 1 {
		return nil
	}
	if got == "" {
		// Not a guess: the client does not know yet that a PIN is needed.
		// Counting these would let one slider drag lock everybody out.
		return errPin
	}
	keep := p.failures[:0]
	for _, t := range p.failures {
		if now.Sub(t) < pinWindow {
			keep = append(keep, t)
		}
	}
	p.failures = append(keep, now)
	if len(p.failures) >= pinMaxFailures {
		p.locked, p.failures = now.Add(pinLockout), nil
		return errPinLocked
	}
	return errPin
}

// change replaces the PIN ("" removes it) after verifying the current one.
func (p *pinStore) change(current, next string) error {
	if err := p.verify(current); err != nil {
		return err
	}
	next = strings.TrimSpace(next)
	if !validPIN(next) {
		return sdk.NewError(http.StatusBadRequest, "invalid_pin", "PIN must be at most 64 printable ASCII characters")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.path == "":
	case next == "":
		if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	default:
		if err := sdk.WriteFileAtomic(p.path, []byte(next+"\n"), 0600); err != nil {
			return err
		}
	}
	p.pin = next
	return nil
}

// validPIN allows printable ASCII only: browsers send header values as
// Latin-1 bytes, so other characters could never be entered from the UI.
func validPIN(s string) bool {
	if len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// adopt stores pin as the PIN if none is set yet (migration from the old
// --pin flag). It reports whether the PIN was taken over.
func (p *pinStore) adopt(pin string) (bool, error) {
	pin = strings.TrimSpace(pin)
	if pin == "" || p.set() {
		return false, nil
	}
	if !validPIN(pin) {
		return false, sdk.NewError(http.StatusBadRequest, "invalid_pin", "PIN must be at most 64 printable ASCII characters")
	}
	return true, p.change("", pin)
}
