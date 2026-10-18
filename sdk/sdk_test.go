package sdk

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCSRF(t *testing.T) {
	h := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, c := range []struct {
		method, header string
		want           int
	}{{"GET", "", 204}, {"POST", "", 403}, {"POST", "1", 204}, {"DELETE", "", 403}} {
		req := httptest.NewRequest(c.method, "/", nil)
		if c.header != "" {
			req.Header.Set(APIHeader, c.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s header=%q: %d want %d", c.method, c.header, rec.Code, c.want)
		}
	}
}

func TestSaveJSONIsAtomicUnderConcurrency(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	var v struct{ A int }
	if err := LoadJSON(p, &v); err != nil || v.A != 0 {
		t.Fatalf("missing file: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := map[string]interface{}{"A": i}
			if i%2 == 0 {
				payload["pad"] = string(make([]byte, 4096)) // differently sized writers
			}
			if err := SaveJSON(p, payload); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if err := LoadJSON(p, &v); err != nil {
		t.Fatalf("file corrupted by concurrent saves: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".s.json.*"))
	if len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestWriteJSONReportsEncodingErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]float64{"x": math.NaN()})
	var body struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 500 || body.Code != "internal" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	WriteError(rec, NewError(409, "stale_epoch", "reload"))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 409 || body.Code != "stale_epoch" {
		t.Fatalf("WriteError: %d %s", rec.Code, rec.Body)
	}
}

func TestStatePathMigratesFromPluginDir(t *testing.T) {
	plugin, data := t.TempDir(), t.TempDir()
	t.Setenv("RCPLUG_PLUGIN_DIR", plugin)
	t.Setenv("RCPLUG_DATA_DIR", data)
	if err := os.WriteFile(filepath.Join(plugin, "x.state.json"), []byte(`{"A":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	p := StatePath("x.state.json")
	if p != filepath.Join(data, "x.state.json") {
		t.Fatalf("path %s", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("old state not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plugin, "x.state.json")); !os.IsNotExist(err) {
		t.Fatal("old state file still in the plugin dir")
	}
}

func TestEnvFallbacks(t *testing.T) {
	t.Setenv("RCPLUG_PORT", "")
	t.Setenv("RCPLUG_HOST_URL", "")
	if ListenAddr("127.0.0.1:1") != "127.0.0.1:1" || HostURL() != "http://127.0.0.1:8080" {
		t.Fatal("fallbacks")
	}
	t.Setenv("RCPLUG_PORT", "9123")
	if ListenAddr("x") != "127.0.0.1:9123" {
		t.Fatal(ListenAddr("x"))
	}
}

// Serve must shut down promptly even with a streaming request open.
func TestServeShutsDownWithOpenStreams(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeListener(ctx, ln, h) }()
	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("shutdown took %s", d)
	}
}
