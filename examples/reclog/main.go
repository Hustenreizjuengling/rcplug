// Command reclog is an example rcplug plugin: it watches the desk's recorder
// through the console plugin and keeps a log of every recording (start, end,
// length, show name). It shows the parts most plugins need:
//
//   - reading the host environment with package sdk (port, data directory),
//   - reading the desk with the console client (package sdk/console),
//   - persisting state atomically in the data directory,
//   - serving an embedded web UI and a small JSON API on loopback.
//
// Build it with "make examples" and copy dist/examples/reclog/ to
// <root>/plugins/reclog/ on the device (or use the wizard's --plugin option).
package main

import (
	"context"
	"embed"
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

// Entry is one finished or running recording.
type Entry struct {
	Show    string     `json:"show"`
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end,omitempty"` // nil while recording
	Seconds float64    `json:"seconds"`
}

// maxEntries bounds the log so the data file stays small.
const maxEntries = 200

type recLog struct {
	path string

	mu      sync.Mutex
	Entries []Entry `json:"entries"`
	live    string  // console connection state, for the UI
}

func main() {
	addr := flag.String("addr", sdk.ListenAddr("127.0.0.1:8090"), "listen address")
	interval := flag.Duration("interval", time.Second, "how often to read the recorder state")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	_ = sdk.LowPriority()

	l := &recLog{path: sdk.StatePath("log.json")}
	if err := sdk.LoadJSON(l.path, l); err != nil {
		log.Printf("starting with an empty log: %v", err)
	}

	ctx, stop := sdk.SignalContext()
	defer stop()
	go l.watch(ctx, capi.New(""), *interval)

	sub, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/log", l.handleLog)
	mux.HandleFunc("POST /api/clear", l.handleClear)

	log.Printf("reclog %s on %s, log file %s", version, *addr, l.path)
	if err := sdk.Serve(ctx, *addr, mux); err != nil {
		log.Fatal(err)
	}
}

// watch polls the console summary and turns recorder transitions into log
// entries. The console may start later than this plugin or restart; errors
// are shown in the UI and polling simply continues.
func (l *recLog) watch(ctx context.Context, c *capi.Client, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	// An entry left open by a previous run is closed on the first poll if
	// the recording has ended meanwhile, or continued if it is still running.
	l.mu.Lock()
	recording := len(l.Entries) > 0 && l.Entries[len(l.Entries)-1].End == nil
	l.mu.Unlock()
	for {
		s, err := c.Summary(ctx)
		switch {
		case err != nil:
			l.setLive("console unavailable: " + err.Error())
		case s.Stale:
			l.setLive("desk offline (last known state)")
		default:
			l.setLive("live")
			now := s.Recorder.State == 2 // 2 = recording
			if now != recording {
				l.transition(now, s.Show, time.Now())
				recording = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (l *recLog) setLive(s string) {
	l.mu.Lock()
	l.live = s
	l.mu.Unlock()
}

// transition opens or closes the newest entry and saves the log.
func (l *recLog) transition(recording bool, show string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.Entries)
	open := n > 0 && l.Entries[n-1].End == nil
	switch {
	case recording && !open:
		l.Entries = append(l.Entries, Entry{Show: show, Start: at})
		if len(l.Entries) > maxEntries {
			l.Entries = l.Entries[len(l.Entries)-maxEntries:]
		}
	case !recording && open:
		e := &l.Entries[n-1]
		e.End, e.Seconds = &at, at.Sub(e.Start).Seconds()
	default:
		return
	}
	if err := sdk.SaveJSON(l.path, l); err != nil {
		log.Printf("saving %s: %v", l.path, err)
	}
}

func (l *recLog) handleLog(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"live": l.live, "entries": l.Entries})
}

// handleClear empties the log. sdk.Serve rejects POSTs without the
// X-Rcplug header, so other web pages cannot trigger it.
func (l *recLog) handleClear(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Entries = nil
	if err := sdk.SaveJSON(l.path, l); err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
