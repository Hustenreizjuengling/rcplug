// Command onair is an example rcplug plugin and the result of the guide in
// docs/plugin-guide.md: a big on-air clock for a tablet or a second screen
// next to the desk. It shows whether the desk is recording, for how long,
// and warns when a time limit set on its page is reached.
//
// It shows the parts most plugins with a web UI need:
//
//   - reading the desk through the console plugin (package sdk/console),
//   - pushing changes to the browser with Server-Sent Events,
//   - a setting that the page changes and the plugin keeps in its data
//     directory,
//   - an embedded web page in two languages, with relative URLs.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

var version = "dev"

//go:embed web
var webFS embed.FS

// View is what the page shows. It is sent whole whenever something changes.
type View struct {
	Recording bool   `json:"recording"`
	Paused    bool   `json:"paused"`
	TimeMs    int64  `json:"timeMs"` // recording time the desk reported
	Show      string `json:"show"`
	Desk      string `json:"desk"` // live, offline (last known state) or unavailable
	LimitMin  int    `json:"limitMin"`
}

// Settings are kept in the data directory.
type Settings struct {
	LimitMin int `json:"limitMin"` // 0 = no limit
}

type app struct {
	path string // settings file

	mu   sync.Mutex
	view View
	subs map[chan struct{}]bool
}

func newApp(path string) *app {
	a := &app{path: path, subs: map[chan struct{}]bool{}}
	var s Settings
	if err := sdk.LoadJSON(path, &s); err != nil {
		log.Printf("settings: %v (using defaults)", err)
	}
	a.view.LimitMin = s.LimitMin
	a.view.Desk = "unavailable"
	return a
}

func main() {
	addr := flag.String("addr", sdk.ListenAddr("127.0.0.1:8091"), "listen address")
	every := flag.Duration("interval", 500*time.Millisecond, "how often to read the recorder")
	showVer := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	_ = sdk.LowPriority() // already done when the host starts us; harmless twice

	a := newApp(sdk.StatePath("settings.json"))
	ctx, stop := sdk.SignalContext()
	defer stop()
	go a.watch(ctx, capi.New(""), *every)

	log.Printf("onair %s on %s", version, *addr)
	if err := sdk.Serve(ctx, *addr, a.routes()); err != nil {
		log.Fatal(err)
	}
}

func (a *app) routes() http.Handler {
	page, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", sdk.Files(page))
	mux.HandleFunc("GET /api/view", a.handleView)
	mux.HandleFunc("GET /api/events", a.handleEvents)
	mux.HandleFunc("POST /api/settings", a.handleSettings)
	return mux
}

// watch reads the recorder from the console's summary. The console may start
// after this plugin or restart; the page shows that, and reading goes on.
func (a *app) watch(ctx context.Context, c *capi.Client, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s, err := c.Summary(ctx)
		a.change(func(v *View) {
			if err != nil {
				v.Desk = "unavailable"
				return
			}
			v.Recording = s.Recorder.State == 2 // 0 ready, 1 paused, 2 recording
			v.Paused = s.Recorder.State == 1
			v.TimeMs = s.Recorder.TimeMs
			v.Show = s.Show
			v.Desk = "live"
			if s.Stale {
				v.Desk = "offline"
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// change applies fn to the view and wakes the browsers if anything changed.
func (a *app) change(fn func(*View)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.view
	fn(&a.view)
	if a.view == old {
		return
	}
	for ch := range a.subs {
		select {
		case ch <- struct{}{}:
		default: // already woken; it sends the latest view anyway
		}
	}
}

func (a *app) snapshot() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.view
}

func (a *app) handleView(w http.ResponseWriter, r *http.Request) {
	sdk.WriteJSON(w, http.StatusOK, a.snapshot())
}

// handleEvents streams the view as Server-Sent Events: the current one at
// once, then every change, and again every 25 s so that idle connections stay
// open. The host's proxy passes the stream through.
func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		sdk.Error(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ch := make(chan struct{}, 1)
	a.mu.Lock()
	a.subs[ch] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.subs, ch)
		a.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	again := time.NewTicker(25 * time.Second)
	defer again.Stop()
	for {
		raw, _ := json.Marshal(a.snapshot())
		fmt.Fprintf(w, "data: %s\n\n", raw)
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-again.C:
		}
	}
}

// handleSettings changes the time limit. sdk.Serve refuses POSTs without the
// X-Rcplug header, so a foreign web page cannot change it.
func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	var s Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&s); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "invalid JSON"))
		return
	}
	if s.LimitMin < 0 || s.LimitMin > 24*60 {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "out_of_range", "limitMin must be 0 to 1440"))
		return
	}
	if err := sdk.SaveJSON(a.path, s); err != nil {
		sdk.WriteError(w, err)
		return
	}
	a.change(func(v *View) { v.LimitMin = s.LimitMin })
	sdk.WriteJSON(w, http.StatusOK, a.snapshot())
}
