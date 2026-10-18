package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

// fakeConsole answers api/summary like the console plugin behind the host.
func fakeConsole(t *testing.T, summary *capi.Summary) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugins/console/api/summary" {
			http.NotFound(w, r)
			return
		}
		sdk.WriteJSON(w, http.StatusOK, summary)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitFor polls cond until it holds or 5 s have passed.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestWatchFollowsTheRecorder(t *testing.T) {
	sum := &capi.Summary{Show: "Episode 7"}
	sum.Recorder.State, sum.Recorder.TimeMs = 2, 61000
	console := fakeConsole(t, sum)
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watch(ctx, capi.New(console.URL), 10*time.Millisecond)

	waitFor(t, "the desk", func() bool { return a.snapshot().Desk == "live" })
	if v := a.snapshot(); !v.Recording || v.TimeMs != 61000 || v.Show != "Episode 7" {
		t.Fatalf("view = %+v", v)
	}
	console.Close() // the console stops
	waitFor(t, "the console to be missed", func() bool { return a.snapshot().Desk == "unavailable" })
}

func TestSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newApp(path)
	h := sdk.CSRF(a.routes()) // as sdk.Serve wraps it

	post := func(body string, header bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		if header {
			req.Header.Set(sdk.APIHeader, "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"limitMin": 30}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without X-Rcplug: %d", rec.Code)
	}
	if rec := post(`{"limitMin": -1}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative limit: %d", rec.Code)
	}
	if rec := post(`{"limitMin": 30}`, true); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}
	if v := newApp(path).snapshot(); v.LimitMin != 30 {
		t.Fatalf("the limit was not kept: %+v", v)
	}
}

func TestEventsStream(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	lines := bufio.NewScanner(resp.Body)
	next := func() View {
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				var v View
				if err := json.Unmarshal([]byte(data), &v); err != nil {
					t.Fatal(err)
				}
				return v
			}
		}
		t.Fatal("stream ended")
		return View{}
	}
	if v := next(); v.Recording {
		t.Fatalf("first view = %+v", v)
	}
	a.change(func(v *View) { v.Recording, v.TimeMs = true, 1000 })
	if v := next(); !v.Recording || v.TimeMs != 1000 {
		t.Fatalf("after the change: %+v", v)
	}
}
