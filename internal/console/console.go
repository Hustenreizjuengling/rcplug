// Package console is the rcplug "console" plugin: it serves the web UI and
// the JSON/SSE API that mirror the desk's whole state to browsers and, under
// a write policy, let them control it. The connection to the desk itself is
// package desk.
package console

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
	"github.com/Hustenreizjuengling/rcplug/sdk/ui"
)

//go:embed web
var webFS embed.FS

// Options configures a Console.
type Options struct {
	Client *desk.Client
	// DataDir keeps settings.json and the PIN file ("" = in memory only).
	DataDir string
	// Panel emulates front-panel input (hw.Panel), used wherever its tty
	// exists: reading along it is how the console changes the desk, and
	// physical faders are always set there.
	Panel FrontPanel
	// FullControl: on the device, take the mixer's place and serve the
	// device's screen through the console, so every setting can be written
	// (desk/relay.go); if that is not possible, read along.
	FullControl bool
	// Meters reads the levels and hardware fader positions the mixer keeps
	// in shared memory (hw.Meters), which never travel over the control
	// port; nil = not available.
	Meters MeterSource
	// Hosted: the console runs under rcplugd, which names the plugins that
	// call it (who.go); requests straight to its port come from the device.
	Hosted bool
	// Publish puts desk events on the host's bus (nil: sdk.Publish).
	Publish func(ctx context.Context, typ string, data interface{}) error
	// AllowUntested offers control on models without a verified profile.
	AllowUntested bool
	// LegacyPIN is a PIN from the old --pin flag or RCPLUG_CONSOLE_PIN. It is
	// taken over once if no PIN is stored yet.
	LegacyPIN string
	Version   string
	Logf      func(format string, args ...interface{})
}

// AuditEntry records one write sent to the desk.
type AuditEntry struct {
	Time   string `json:"time"`
	Node   string `json:"node"`
	Path   []int  `json:"path"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Remote string `json:"remote"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
}

// Console serves the console's UI and API.
type Console struct {
	opts   Options
	client *desk.Client
	mirror *desk.Mirror
	nudge  chan struct{} // a control change waits to be broadcast
	pin    *pinStore

	mu            sync.Mutex
	settings      Settings
	audit         []AuditEntry
	lastReconnect time.Time
	clientsAt     time.Time
	clientsDoc    interface{}
	meters        meterState
	reset         pinReset
	presence      presenceState
	desk          *deskEvents
	changes       *changeLog
	held          map[string]*heldPress // button presses of api/press not released yet
	pressGen      uint64
}

// New creates the console and loads persisted settings.
func New(o Options) *Console {
	if o.Logf == nil {
		o.Logf = func(string, ...interface{}) {}
	}
	c := &Console{opts: o, client: o.Client, mirror: o.Client.Mirror(), nudge: make(chan struct{}, 1), pin: newPinStore(o.DataDir), desk: newDeskEvents(o.Publish),
		changes: newChangeLog()}
	if o.DataDir != "" {
		if err := sdk.LoadJSON(c.settingsPath(), &c.settings); err != nil {
			o.Logf("console: settings: %v", err)
		}
	}
	if o.LegacyPIN != "" {
		switch ok, err := c.pin.adopt(o.LegacyPIN); {
		case err != nil:
			o.Logf("console: cannot take over the old PIN: %v", err)
		case ok:
			o.Logf("console: took over the PIN from --pin/RCPLUG_CONSOLE_PIN; remove it there, the PIN is now stored on the device")
		default:
			o.Logf("console: ignoring --pin/RCPLUG_CONSOLE_PIN, a PIN is already stored (change it in the UI)")
		}
	}
	if _, err := ParseLevel(c.settings.Control); err != nil {
		c.settings.Control = "off"
	}
	if o.FullControl {
		c.client.SetFullControl(true) // does nothing where the console does not read along
	}
	c.mirror.Watch(c.watchPinReset)
	c.mirror.Watch(c.watchPresence)
	c.mirror.Watch(c.watchDesk)
	c.mirror.WatchChanges(c.watchChanges)
	c.client.OnStatus(func(st desk.Status) {
		data, _ := json.Marshal(st)
		c.mirror.Notify("status", data)
		c.connectionEvent(st)
	})
	return c
}

func (c *Console) settingsPath() string { return filepath.Join(c.opts.DataDir, "settings.json") }

func (c *Console) saveSettingsLocked() {
	if c.opts.DataDir == "" {
		return
	}
	if err := sdk.SaveJSON(c.settingsPath(), c.settings); err != nil {
		c.opts.Logf("console: saving settings: %v", err)
	}
}

// Level returns the effective control level and, when it is limited, why
// ("read_only" or "untested_model").
func (c *Console) Level() (Level, string) {
	if c.readOnly() {
		return LevelOff, "read_only"
	}
	controllable := true
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout != nil {
			controllable = s.Layout.Controllable
		}
	})
	if !controllable && !c.opts.AllowUntested {
		return LevelOff, "untested_model"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, _ := ParseLevel(c.settings.Control)
	return l, ""
}

// Run broadcasts coalesced changes to live clients until ctx ends: every
// 60 ms, and within a few milliseconds of a control change (a button, a
// fader), so a screen plugin answers the desk as promptly as RØDE's screen.
func (c *Console) Run(ctx context.Context) {
	go c.runMeters(ctx)
	go c.runDeskEvents(ctx)
	go c.runChangeLog(ctx)
	t := time.NewTicker(60 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.mirror.Broadcast(Secret)
		case <-c.nudge:
			time.Sleep(3 * time.Millisecond) // the rest of the same batch of changes
			c.mirror.Broadcast(Secret)
		}
	}
}

// Handler returns the console's HTTP handler (UI + API). Wrap it in
// sdk.CSRF (sdk.Serve does).
func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	c.register(mux)
	mux.Handle("GET /ui/", ui.Handler("/ui/")) // rcplug's shared look (sdk/ui)
	sub, _ := fs.Sub(webFS, "web")
	files := sdk.Files(sub) // kept by browsers (ETag), compressed
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
	return mux
}

// FrontPanel presses the device's buttons (hw.Panel; tests use a fake).
type FrontPanel interface {
	Do(id string, value int) error
	Available() error
}

// readOnly: --read-only or the user's switch.
func (c *Console) readOnly() bool {
	if c.client.ReadOnly() {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.ReadOnly
}

// panelOn reports whether the device's buttons can be pressed (the panel's
// tty exists).
func (c *Console) panelOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.panelOnLocked()
}

func (c *Console) panelOnLocked() bool {
	return c.opts.Panel != nil && c.opts.Panel.Available() == nil
}

// statusDoc is GET api/status and the SSE hello event.
func (c *Console) statusDoc() map[string]interface{} {
	level, limit := c.Level()
	c.mu.Lock()
	panel := c.panelOnLocked()
	c.mu.Unlock()
	doc := map[string]interface{}{
		"connection":   c.client.Status(),
		"control":      level.String(),
		"controlLimit": limit,
		"readOnly":     c.readOnly(),
		"readOnlyFlag": c.client.ReadOnly(),
		"pinReset":     c.pinResetState(),
		"pinRequired":  c.pin.set(),
		"panel":        panel,
		"passive":      c.client.Passive(),
		"meters":       c.metersLive(),
		"version":      c.opts.Version,
	}
	c.mirror.View(func(s desk.Snapshot) {
		doc["epoch"], doc["seq"], doc["stale"] = s.Epoch, s.Seq, s.Stale
		if s.Layout != nil {
			doc["model"], doc["modelName"], doc["firmware"] = s.Layout.Model, s.Layout.ModelName, s.Layout.Firmware
			doc["supported"] = s.Layout.Controllable
		} else {
			doc["model"], doc["supported"] = rcproto.ModelUnknown, false
		}
	})
	return doc
}
