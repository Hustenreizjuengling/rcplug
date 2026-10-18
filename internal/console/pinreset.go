package console

import (
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// A forgotten PIN is reset at the device, without a shell: after a request
// from the console, whoever presses channel 1's Listen (or Mute) button four
// times in quick succession stands in front of it. Over the network these
// buttons can only be pressed through the front panel, the touchscreen or a
// write, all of which need the PIN; four presses within a few seconds do not
// happen by chance while someone uses the desk. An even number of presses
// leaves the button as it was.

const (
	pinResetPresses = 4
	pinResetWindow  = time.Minute // for the presses after the request
)

// pinResetBurst: all presses within this span (tests shorten it).
var pinResetBurst atomic.Int64

func init() { pinResetBurst.Store(int64(4 * time.Second)) }

type pinReset struct {
	mu      sync.Mutex
	path    []int // the channel's buttons (SOLOMUTEBUTTON 0)
	until   time.Time
	presses []time.Time
	doneAt  time.Time
}

// pinResetState is the reset's progress for api/status.
func (c *Console) pinResetState() map[string]interface{} {
	pr := &c.reset
	pr.mu.Lock()
	defer pr.mu.Unlock()
	now := time.Now()
	doc := map[string]interface{}{"active": now.Before(pr.until), "need": pinResetPresses}
	if now.Before(pr.until) {
		doc["presses"] = len(pr.presses)
		doc["seconds"] = int(pr.until.Sub(now).Seconds()) + 1
	}
	if !pr.doneAt.IsZero() && now.Sub(pr.doneAt) < 5*time.Minute {
		doc["done"] = pr.doneAt.Format(time.RFC3339)
	}
	return doc
}

// handlePinReset starts (or restarts) the window for the presses. It needs
// no PIN: that is its point.
func (c *Console) handlePinReset(w http.ResponseWriter, r *http.Request) {
	if !c.pin.set() {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "no_pin", "no PIN is set"))
		return
	}
	var path []int
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout != nil && s.Root != nil {
			if p := s.Layout.Path("SOLOMUTEBUTTON", 0); p != nil && s.Root.At(p) != nil {
				path = p
			}
		}
	})
	if path == nil {
		err := sdk.NewError(http.StatusConflict, "no_buttons", "the device's buttons are not in the mirrored state (yet)")
		c.auditAction(r, "pin reset", "start", err)
		sdk.WriteError(w, err)
		return
	}
	pr := &c.reset
	pr.mu.Lock()
	pr.path, pr.presses, pr.until = path, nil, time.Now().Add(pinResetWindow)
	pr.mu.Unlock()
	c.opts.Logf("console: PIN reset started by %s; waiting for %d presses at the device", remoteOf(r), pinResetPresses)
	c.auditAction(r, "pin reset", "start", nil)
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"need": pinResetPresses, "seconds": int(pinResetWindow.Seconds()), "burstSeconds": int(time.Duration(pinResetBurst.Load()).Seconds())})
}

// watchPinReset counts presses of the channel's buttons (it runs under the
// mirror lock for every change).
func (c *Console) watchPinReset(path []int, name string, v rcproto.Value) {
	if name != "soloPressed" && name != "mutePressed" {
		return
	}
	pr := &c.reset
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if !time.Now().Before(pr.until) || !slices.Equal(path, pr.path) || !v.Truth() {
		return
	}
	now := time.Now()
	keep := pr.presses[:0]
	for _, t := range pr.presses {
		if now.Sub(t) < time.Duration(pinResetBurst.Load()) {
			keep = append(keep, t)
		}
	}
	pr.presses = append(keep, now)
	if len(pr.presses) < pinResetPresses {
		return
	}
	pr.until, pr.doneAt = time.Time{}, time.Now()
	go c.finishPinReset()
}

func (c *Console) finishPinReset() {
	err := c.pin.reset()
	c.mu.Lock()
	c.settings.Control = LevelOff.String() // start again from the safe level
	c.saveSettingsLocked()
	c.mu.Unlock()
	e := AuditEntry{Time: time.Now().Format("15:04:05"), Node: "console", Path: []int{}, Name: "pin reset", Value: "removed at the device", Remote: "device"}
	if err != nil {
		e.Error = err.Error()
		c.opts.Logf("console: PIN reset at the device failed: %v", err)
	} else {
		c.opts.Logf("console: PIN removed at the device (buttons pressed %d times); control level set to off", pinResetPresses)
	}
	c.appendAudit(e)
	c.notifyControl()
}
