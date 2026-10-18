package host

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallFromTheDashboard(t *testing.T) {
	var got []string
	old := spawnUninstall
	spawnUninstall = func(initDir, root string, links bool) error {
		got = []string{initDir, root}
		return nil
	}
	t.Cleanup(func() { spawnUninstall = old })
	post := func(root, body string) *httptest.ResponseRecorder {
		m := NewManager(root, 19950)
		h := NewServer(m, nil, "test").Handler()
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/uninstall", strings.NewReader(body))
		req.Header.Set("X-Rcplug", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// A development tree (make run-local runs from dist/local) is never removed.
	dev := filepath.Join(t.TempDir(), "local")
	os.MkdirAll(dev, 0o755)
	os.WriteFile(filepath.Join(dev, "rcplugd"), []byte("x"), 0o755)
	if rec := post(dev, `{"confirm":"uninstall"}`); rec.Code != 409 || got != nil {
		t.Fatalf("development tree: %d %s", rec.Code, rec.Body)
	}
	inst := filepath.Join(t.TempDir(), "rcplug")
	os.MkdirAll(inst, 0o755)
	if rec := post(inst, `{"confirm":"uninstall"}`); rec.Code != 409 {
		t.Fatalf("without rcplugd: %d", rec.Code)
	}
	os.WriteFile(filepath.Join(inst, "rcplugd"), []byte("x"), 0o755)
	if rec := post(inst, `{}`); rec.Code != 400 || got != nil {
		t.Fatalf("without confirmation: %d", rec.Code)
	}
	t.Setenv("RCPLUG_INITDIR", "/sandbox/initd")
	if rec := post(inst, `{"confirm":"uninstall"}`); rec.Code != 200 || got[0] != "/sandbox/initd" || got[1] != inst {
		t.Fatalf("uninstall: %d %v", rec.Code, got)
	}
}
