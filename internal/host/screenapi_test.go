package host

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The screen API takes only plugin tokens; the state endpoint answers
// anyone.
func TestScreenAPIAuth(t *testing.T) {
	s := &Server{m: &Manager{}, Events: NewBus()}
	r := httptest.NewRequest(http.MethodPost, "/api/screen/take", nil)
	w := httptest.NewRecorder()
	s.handleScreenTake(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("take without a token: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/screen/take", nil)
	r.Header.Set("X-Rcplug-Token", s.Events.Token("nobody"))
	w = httptest.NewRecorder()
	s.handleScreenTake(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("take by an unknown plugin: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.handleScreenState(w, httptest.NewRequest(http.MethodGet, "/api/screen", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("state: %d %s", w.Code, w.Body)
	}
}

// A plugin that takes the screen on request runs like any plugin, gets the
// screen when it asks, gives it back without ending, and the wish survives
// a restart of rcplug (the screen comes back to it once it runs).
func TestScreenOnRequest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "webui",
		"#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile :; do sleep 0.1; done\n",
		`{"name":"webui","title":"Web UI","version":"1.0","exec":["./run.sh"],"autostart":true,"permissions":["screen:request","desk:write"]}`)
	ts := newTestScreen(t)
	m := NewManager(root, 9310)
	m.Screen = ts.Screen
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	waitFor(t, "running without the screen", 3*time.Second, func() bool {
		s, _ := statusOf(m, "webui")
		return s.Running
	})
	if ts.Holder() != "" {
		t.Fatal("an autostarted plugin must not take the screen by itself")
	}
	if err := m.TakeScreenFor("webui"); err != nil {
		t.Fatal(err)
	}
	if holder, wanted := m.ScreenState(); holder != "webui" || wanted != "webui" {
		t.Fatalf("after take: holder %q wanted %q", holder, wanted)
	}
	// A second manager over the same state (rcplug restarted): the plugin
	// comes up and the screen comes back to it.
	m.StopAll()
	ts.Release("") // rcplug's start hands a left-over screen back first
	m2 := NewManager(root, 9311)
	m2.Screen = ts.Screen
	if _, err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	defer m2.StopAll()
	waitFor(t, "the screen back after a restart", 10*time.Second, func() bool { return ts.Holder() == "webui" })
	m2.ReleaseScreenFor("webui")
	if holder, wanted := m2.ScreenState(); holder != "" || wanted != "" {
		t.Fatalf("after release: holder %q wanted %q", holder, wanted)
	}
	s, _ := statusOf(m2, "webui")
	if !s.Running {
		t.Fatal("releasing the screen must not stop the plugin")
	}
}
