package console

import (
	"net"
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

// Presence for the host: rcplugd's dashboard password is reset the same way
// (four quick presses of channel 1's Listen button), and the console, which
// sees the buttons, confirms them. Only the host may ask, directly on the
// loopback interface: a request through its proxy (from the network) is
// refused, so no visitor can confirm presses for the host.

type presenceWait struct {
	path    []int
	presses []time.Time
	done    chan struct{}
	once    sync.Once
}

type presenceState struct {
	mu      sync.Mutex
	waiters map[*presenceWait]struct{}
}

func (c *Console) watchPresence(path []int, name string, v rcproto.Value) {
	if (name != "soloPressed" && name != "mutePressed") || !v.Truth() {
		return
	}
	c.presence.mu.Lock()
	defer c.presence.mu.Unlock()
	now := time.Now()
	for w := range c.presence.waiters {
		if !slices.Equal(path, w.path) {
			continue
		}
		keep := w.presses[:0]
		for _, t := range w.presses {
			if now.Sub(t) < time.Duration(pinResetBurst.Load()) {
				keep = append(keep, t)
			}
		}
		w.presses = append(keep, now)
		if len(w.presses) >= pinResetPresses {
			w.once.Do(func() { close(w.done) })
		}
	}
}

// handlePresence: POST api/presence waits up to a minute for the presses
// and answers {"ok": true}, or 408.
func (c *Console) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Forwarded-For") != "" || !loopback(r.RemoteAddr) {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "host_only", "only rcplugd may ask for presses at the device"))
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
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "no_buttons", "the device's buttons are not in the mirrored state (yet)"))
		return
	}
	wt := &presenceWait{path: path, done: make(chan struct{})}
	c.presence.mu.Lock()
	if c.presence.waiters == nil {
		c.presence.waiters = map[*presenceWait]struct{}{}
	}
	c.presence.waiters[wt] = struct{}{}
	c.presence.mu.Unlock()
	defer func() {
		c.presence.mu.Lock()
		delete(c.presence.waiters, wt)
		c.presence.mu.Unlock()
	}()
	c.opts.Logf("console: rcplugd waits for %d presses at the device (dashboard password reset)", pinResetPresses)
	t := time.NewTimer(pinResetWindow)
	defer t.Stop()
	select {
	case <-wt.done:
		c.opts.Logf("console: presses at the device confirmed to rcplugd")
		sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case <-t.C:
		sdk.WriteError(w, sdk.NewError(http.StatusRequestTimeout, "timeout", "time is up; nothing was changed"))
	case <-r.Context().Done():
	}
}

func loopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
