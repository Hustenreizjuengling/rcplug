package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const optionsManifest = `{"exec":["./run.sh"],"autostart":true,"options":[
 {"id":"meters","type":"choice","title":"Meters","default":"auto","choices":[{"value":"auto","title":"Auto"},{"value":"off","title":"Off"}],"arg":"--meters={value}"},
 {"id":"readOnly","type":"bool","title":"Read only","arg":"--read-only"},
 {"id":"fps","type":"number","title":"Frames","min":0.5,"max":10,"arg":"--fps={value}"},
 {"id":"name","type":"text","title":"Name","maxLength":12,"pattern":"[a-z ]+","env":"PLUGIN_NAME"},
 {"id":"verbose","type":"bool","title":"Verbose","env":"VERBOSE"}
],"translations":{"de":{"options":{"meters":{"title":"Pegel","choices":{"off":"Aus"}}}}}}`

func TestOptionDefinitionsAreChecked(t *testing.T) {
	for _, bad := range []string{
		`{"id":"1x","type":"bool","title":"T","arg":"--x"}`,
		`{"id":"x","type":"bool","title":"","arg":"--x"}`,
		`{"id":"x","type":"bool","title":"T"}`,
		`{"id":"x","type":"bool","title":"T","arg":"--x","env":"X"}`,
		`{"id":"x","type":"bool","title":"T","arg":"--x={value}"}`,
		`{"id":"x","type":"bool","title":"T","arg":"x; rm -rf /"}`,
		`{"id":"x","type":"text","title":"T","arg":"--x"}`,
		`{"id":"x","type":"text","title":"T","arg":"{value}"}`,
		`{"id":"x","type":"text","title":"T","env":"RCPLUG_PORT"}`,
		`{"id":"x","type":"text","title":"T","env":"lower"}`,
		`{"id":"x","type":"choice","title":"T","arg":"--x={value}"}`,
		`{"id":"x","type":"choice","title":"T","arg":"--x={value}","choices":[{"value":"a","title":"A"},{"value":"a","title":"B"}]}`,
		`{"id":"x","type":"number","title":"T","arg":"--x={value}","min":5,"max":1}`,
		`{"id":"x","type":"number","title":"T","arg":"--x={value}","max":1,"default":3}`,
		`{"id":"x","type":"text","title":"T","arg":"--x={value}","pattern":"("}`,
		`{"id":"x","type":"color","title":"T","arg":"--x={value}"}`,
	} {
		root := t.TempDir()
		writePlugin(t, root, "p", "#!/bin/sh\n", `{"exec":["./run.sh"],"options":[`+bad+`]}`)
		if _, err := LoadManifest(filepath.Join(root, "plugins", "p")); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	root := t.TempDir()
	writePlugin(t, root, "p", "#!/bin/sh\n", optionsManifest)
	if _, err := LoadManifest(filepath.Join(root, "plugins", "p")); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsBecomeArgumentsAndEnvironment(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "opt", "#!/bin/sh\necho \"args=$* name=$PLUGIN_NAME verbose=$VERBOSE\"\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n", optionsManifest)
	m := NewManager(root, 9650)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	s := NewServer(m, nil, "test")
	h := s.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/plugins/opt/options", strings.NewReader(body))
		req.Header.Set("X-Rcplug", "1")
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for body, code := range map[string]string{
		`{"values":{"meters":"loud"}}`:      "invalid_option",
		`{"values":{"fps":11}}`:             "invalid_option",
		`{"values":{"name":"Ab"}}`:          "invalid_option",
		`{"values":{"name":"a\nb"}}`:        "invalid_option",
		`{"values":{"readOnly":"yes"}}`:     "invalid_option",
		`{"values":{"unknown":true}}`:       "unknown_option",
		`{"values":{"meters":"off"},"x":1}`: "bad_request",
	} {
		if rec := post(body); rec.Code != 400 || !strings.Contains(rec.Body.String(), code) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "config", "opt.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the config: %v", err)
	}
	// Existing expert args survive.
	_ = os.MkdirAll(filepath.Join(root, "config"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "config", "opt.json"), []byte(`{"args":["--extra"]}`), 0o644)
	rec := post(`{"values":{"meters":"off","readOnly":true,"fps":2.5,"name":"studio a","verbose":true}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restarted":true`) {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, "restart with options", 5*time.Second, func() bool {
		for _, l := range m.Logs("opt") {
			if strings.Contains(l, "args=--meters=off --read-only --fps=2.5 --extra name=studio a verbose=1") {
				return true
			}
		}
		return false
	})
	raw, _ := os.ReadFile(filepath.Join(root, "config", "opt.json"))
	var cfg UserConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Args) != 1 || string(cfg.Options["fps"]) != "2.5" {
		t.Fatalf("config: %s (%v)", raw, err)
	}
	// GET returns the values; null resets one.
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/opt/options", nil)
	req.Host = "127.0.0.1"
	get := httptest.NewRecorder()
	h.ServeHTTP(get, req)
	if !strings.Contains(get.Body.String(), `"meters":"off"`) {
		t.Fatalf("get: %s", get.Body)
	}
	if rec := post(`{"values":{"readOnly":null,"verbose":false}}`); rec.Code != 200 {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, "restart without read-only", 5*time.Second, func() bool {
		for _, l := range m.Logs("opt") {
			if strings.Contains(l, "args=--meters=off --fps=2.5 --extra name=studio a verbose=0") {
				return true
			}
		}
		return false
	})
	// The dashboard gets the options with the plugin list.
	if st, _ := statusOf(m, "opt"); len(st.Options) != 5 || st.Translations["de"].Options["meters"].Choices["off"] != "Aus" {
		t.Fatalf("status options: %+v", st.Options)
	}
}
