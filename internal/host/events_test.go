package host

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEventBusRules(t *testing.T) {
	b := NewBus()
	for _, bad := range []string{"", "nodot", "Desk.Upper", "a..b", ".a.b", "a.b.c.d.e.f.g", "a b.c"} {
		if _, err := b.Publish("x", bad, nil); err == nil {
			t.Errorf("type %q accepted", bad)
		}
	}
	if _, err := b.Publish("x", "a.b", json.RawMessage(`[1,2]`)); err == nil {
		t.Error("data that is not an object accepted")
	}
	if _, err := b.Publish("x", "a.b", map[string]string{"big": strings.Repeat("x", eventDataMax)}); err == nil {
		t.Error("oversized data accepted")
	}
	for i := 0; i < eventsKept+10; i++ {
		if _, err := b.Publish("x", "a.b", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	all := b.Since(0, nil)
	if len(all) != eventsKept || all[0].ID != 11 {
		t.Fatalf("kept %d, first %d", len(all), all[0].ID)
	}
	if got := b.Since(all[len(all)-2].ID, []string{"a."}); len(got) != 1 {
		t.Fatalf("since: %d", len(got))
	}
	if got := b.Since(0, []string{"other."}); len(got) != 0 {
		t.Fatalf("filter: %d", len(got))
	}
}

func TestEventsOverHTTP(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "talker", "#!/bin/sh\necho \"token=${#RCPLUG_TOKEN}\"\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n", `{"exec":["./run.sh"],"autostart":true}`)
	m := NewManager(root, 19600)
	m.Events = NewBus()
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	srv := NewServer(m, nil, "test")
	srv.Events = m.Events
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	waitFor(t, "the plugin's token", 5*time.Second, func() bool {
		return strings.Contains(strings.Join(m.Logs("talker"), "\n"), "token=32")
	})
	waitFor(t, "a host event for the plugin", 5*time.Second, func() bool {
		for _, e := range m.Events.Since(0, []string{"host.plugin."}) {
			if strings.Contains(string(e.Data), `"name":"talker"`) && strings.Contains(string(e.Data), `"state":"running"`) {
				return true
			}
		}
		return false
	})

	// A stream for onair.* only.
	req, _ := http.NewRequest("GET", hs.URL+"/api/events/stream?type=onair.", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)

	publish := func(token, body string) *http.Response {
		r, _ := http.NewRequest("POST", hs.URL+"/api/events", strings.NewReader(body))
		r.Header.Set("X-Rcplug", "1")
		if token != "" {
			r.Header.Set("X-Rcplug-Token", token)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := publish("", `{"type":"onair.segment.started"}`); res.StatusCode != 403 {
		t.Fatalf("without a token: %d", res.StatusCode)
	}
	token := m.Events.Token("talker") // as the host hands it at the next start
	if res := publish(token, `{"type":"host.plugin.state"}`); res.StatusCode != 403 {
		t.Fatalf("a host event from a plugin: %d", res.StatusCode)
	}
	if res := publish(token, `{"type":"other.thing"}`); res.StatusCode != 200 {
		t.Fatalf("publish other: %d", res.StatusCode)
	}
	if res := publish(token, `{"type":"onair.segment.started","data":{"title":"Intro"}}`); res.StatusCode != 200 {
		t.Fatalf("publish: %d", res.StatusCode)
	}
	var got Event
	for lines.Scan() {
		if l := lines.Text(); strings.HasPrefix(l, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &got); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if got.Type != "onair.segment.started" || got.Source != "talker" || !strings.Contains(string(got.Data), "Intro") {
		t.Fatalf("streamed %+v", got)
	}

	// Catching up with Last-Event-ID.
	r2, _ := http.NewRequest("GET", hs.URL+"/api/events/stream?type=other.", nil)
	r2.Header.Set("Last-Event-ID", "0")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	resp2, err := http.DefaultClient.Do(r2.WithContext(ctx2))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	sc2 := bufio.NewScanner(resp2.Body)
	found := false
	for sc2.Scan() {
		if strings.Contains(sc2.Text(), `"type":"other.thing"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no backlog after Last-Event-ID")
	}
	res, err := http.Get(hs.URL + "/api/events?type=onair.")
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Events []Event }
	json.NewDecoder(res.Body).Decode(&list)
	res.Body.Close()
	if len(list.Events) != 1 {
		t.Fatalf("GET api/events: %+v", list)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	b := NewBus()
	sub, _, err := b.subscribe(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < eventSubBacklog+1; i++ {
		b.Publish("x", "a.b", nil)
	}
	select {
	case <-sub.lost:
	default:
		t.Fatal("a subscriber that does not read was kept")
	}
}

func TestProxyNamesThePlugin(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "scenes", "#!/bin/sh\n", `{"exec":["./run.sh"],"permissions":["desk:write","network"]}`)
	m := NewManager(root, 19650)
	m.Events = NewBus()
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	srv := NewServer(m, nil, "test")
	srv.Events = m.Events
	token := m.Events.Token("scenes")
	run := func(remote string, hdr map[string]string) http.Header {
		in := httptest.NewRequest("GET", "http://127.0.0.1/plugins/console/api/status", nil)
		in.RemoteAddr = remote
		for k, v := range hdr {
			in.Header.Set(k, v)
		}
		pr := &httputil.ProxyRequest{In: in, Out: in.Clone(in.Context())}
		srv.identify(pr)
		return pr.Out.Header
	}
	h := run("127.0.0.1:40000", map[string]string{"X-Rcplug-Token": token})
	if h.Get("X-Rcplug-Plugin") != "scenes" || h.Get("X-Rcplug-Permissions") != "desk:write,network" || h.Get("X-Rcplug-Token") != "" {
		t.Fatalf("a plugin: %v", h)
	}
	h = run("127.0.0.1:40000", map[string]string{"X-Rcplug-Token": "wrong"})
	if h.Get("X-Rcplug-Plugin") != "unknown" || h.Get("X-Rcplug-Permissions") != "" {
		t.Fatalf("a wrong token: %v", h)
	}
	h = run("127.0.0.1:40000", map[string]string{"Sec-Fetch-Mode": "cors"})
	if h.Get("X-Rcplug-Plugin") != "" {
		t.Fatalf("a browser on the device: %v", h)
	}
	h = run("192.168.1.30:50000", map[string]string{"X-Rcplug-Plugin": "scenes", "X-Rcplug-Permissions": "desk:write", "X-Rcplug-Token": token})
	if h.Get("X-Rcplug-Plugin") != "" || h.Get("X-Rcplug-Permissions") != "" || h.Get("X-Rcplug-Token") != "" {
		t.Fatalf("forged by the network: %v", h)
	}
	for _, bad := range []string{`["desk"]`, `["audio","audio"]`} {
		writePlugin(t, root, "bad", "#!/bin/sh\n", `{"exec":["./run.sh"],"permissions":`+bad+`}`)
		if _, err := LoadManifest(filepath.Join(root, "plugins", "bad")); err == nil {
			t.Errorf("permissions %s accepted", bad)
		}
	}
}
