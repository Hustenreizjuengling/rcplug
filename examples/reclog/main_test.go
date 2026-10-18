package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

// A fake console answers api/summary with whatever recorder state the test
// sets, so the example's logic is tested without a desk.
func TestWatchLogsRecordings(t *testing.T) {
	state := make(chan int64, 1)
	cur := int64(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case cur = <-state:
		default:
		}
		sdk.WriteJSON(w, 200, capi.Summary{Show: "Episode 7", Recorder: capi.RecorderState{State: cur}})
	}))
	defer srv.Close()

	l := &recLog{path: filepath.Join(t.TempDir(), "log.json")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.watch(ctx, capi.New(srv.URL), 5*time.Millisecond)

	state <- 2 // start recording
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.Entries) == 1 })
	state <- 0 // stop
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.Entries[0].End != nil })

	var saved recLog
	if err := sdk.LoadJSON(l.path, &saved); err != nil || len(saved.Entries) != 1 || saved.Entries[0].Show != "Episode 7" {
		t.Fatalf("saved log = %+v, %v", saved.Entries, err)
	}

	rec := httptest.NewRecorder()
	l.handleLog(rec, httptest.NewRequest("GET", "/api/log", nil))
	var doc struct {
		Live    string  `json:"live"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Live != "live" || len(doc.Entries) != 1 {
		t.Fatalf("api/log = %s", rec.Body)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not reached")
}

// A recording that ended while the plugin was not running is closed at the
// first poll and not merged with the next one.
func TestWatchClosesEntryLeftOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sdk.WriteJSON(w, 200, capi.Summary{Recorder: capi.RecorderState{State: 0}})
	}))
	defer srv.Close()
	l := &recLog{path: filepath.Join(t.TempDir(), "log.json"),
		Entries: []Entry{{Show: "old", Start: time.Now().Add(-time.Hour)}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.watch(ctx, capi.New(srv.URL), 5*time.Millisecond)
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.Entries[0].End != nil })
}
