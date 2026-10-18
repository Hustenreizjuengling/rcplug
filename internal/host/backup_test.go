package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const backupManifest = `{"exec":["./run.sh"],"options":[
 {"id":"meters","type":"choice","title":"Meters","choices":[{"value":"auto","title":"Auto"},{"value":"off","title":"Off"}],"arg":"--meters={value}"},
 {"id":"fps","type":"number","title":"Frames","min":0.5,"max":10,"arg":"--fps={value}"},
 {"id":"apiToken","type":"text","title":"Token","env":"API_TOKEN"}
]}`

func TestBackupAndRestore(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "opt", "#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile true; do sleep 1; done\n", backupManifest)
	m := NewManager(root, 9670)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()
	h := NewServer(m, nil, "1.2.3").Handler()
	call := func(method, path string, body interface{}) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
		req.Header.Set("X-Rcplug", "1")
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if _, _, err := m.SetOptions("opt", map[string]json.RawMessage{"meters": json.RawMessage(`"off"`), "apiToken": json.RawMessage(`"s3cret"`)}); err != nil {
		t.Fatal(err)
	}
	m.Disable("opt")
	m.SetAutoCheck(true)

	rec := call(http.MethodGet, "/api/backup", nil)
	var b RcplugBackup
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &b) != nil {
		t.Fatalf("backup: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("a secret option in the backup: %s", rec.Body)
	}
	if b.Version != "1.2.3" || !b.AutoCheck || len(b.Plugins) != 1 || b.Plugins[0].Enabled == nil || *b.Plugins[0].Enabled ||
		string(b.Plugins[0].Options["meters"]) != `"off"` || len(b.Plugins[0].LeftOut) != 1 {
		t.Fatalf("backup: %s", rec.Body)
	}

	// Change everything, then look at and carry out the restore.
	if _, _, err := m.SetOptions("opt", map[string]json.RawMessage{"meters": json.RawMessage(`"auto"`), "fps": json.RawMessage(`2`)}); err != nil {
		t.Fatal(err)
	}
	m.Enable("opt")
	m.SetAutoCheck(false)
	b.Plugins = append(b.Plugins, BackupPlugin{Name: "gone", Version: "0.1.0"})
	b.Plugins[0].Options["old"] = json.RawMessage(`1`)
	rec = call(http.MethodPost, "/api/backup/diff", b)
	var plan BackupPlan
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("diff: %d %s", rec.Code, rec.Body)
	}
	got := map[string]string{}
	for _, c := range plan.Changes {
		got[c.What+":"+c.ID] = string(c.From) + "→" + string(c.To)
	}
	want := map[string]string{"enabled:": "true→false", "option:meters": `"auto"→"off"`, "option:fps": "2→null", "autoCheck:": "false→true"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("change %s: %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if len(plan.Changes) != len(want) || len(plan.Missing) != 1 || plan.Missing[0].Name != "gone" || len(plan.Skipped) != 1 {
		t.Fatalf("plan: %s", rec.Body)
	}

	if rec = call(http.MethodPost, "/api/backup/restore", b); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"errors":[]`) {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	opts, _, _ := m.Options("opt")
	if string(opts["meters"]) != `"off"` || opts["fps"] != nil || string(opts["apiToken"]) != `"s3cret"` || !m.AutoCheck() {
		t.Fatalf("after the restore: %v autoCheck %v", opts, m.AutoCheck())
	}
	if m.get("opt").status().Enabled {
		t.Fatal("opt still on")
	}
	rec = call(http.MethodPost, "/api/backup/diff", b)
	if json.Unmarshal(rec.Body.Bytes(), &plan) != nil || len(plan.Changes) != 0 {
		t.Fatalf("still different: %s", rec.Body)
	}
	if rec = call(http.MethodPost, "/api/backup/restore", map[string]string{"kind": "something"}); rec.Code != 400 {
		t.Fatalf("not a backup: %d", rec.Code)
	}
}
