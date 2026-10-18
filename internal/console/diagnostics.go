package console

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// handleDiagnostics returns the console's part of the host's diagnostics
// bundle (GET api/diagnostics, a zip): status and connection statistics, the
// clients of the control port, the write log, hardware status, the layout and
// the device state as a sanitized full-sync frame, which "rcsim --fixture"
// can replay.
func (c *Console) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	now := time.Now()
	add := func(name string, data []byte) {
		if f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}); err == nil {
			_, _ = f.Write(data)
		}
	}
	// The read endpoints already produce the right documents (secrets
	// redacted); capture their output.
	for name, h := range map[string]http.HandlerFunc{
		"status.json": c.handleStatus, "clients.json": c.handleClients, "audit.json": c.handleAudit,
		"hw.json": c.handleHW, "summary.json": c.handleSummary,
	} {
		rec := httptest.NewRecorder()
		h(rec, r)
		add(name, rec.Body.Bytes())
	}
	c.mu.Lock()
	settings := map[string]interface{}{"control": c.settings.Control, "screen": c.settings.Screen, "pinSet": c.pin.set(),
		"readOnly": c.client.ReadOnly(), "allowUntested": c.opts.AllowUntested, "panel": c.panelOnLocked(), "screenRotate": c.settings.ScreenRotate}
	c.mu.Unlock()
	raw, _ := json.MarshalIndent(settings, "", "  ")
	add("settings.json", raw)

	var tree *rcproto.Node
	var layout interface{}
	c.mirror.View(func(s desk.Snapshot) {
		if s.Root != nil {
			tree = rcproto.Sanitize(s.Root)
			layout = s.Layout
		}
	})
	if tree != nil {
		raw, _ := json.MarshalIndent(layout, "", "  ")
		add("layout.json", raw)
		add("tree-sanitized.frame", rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: tree}).Encode()))
	}
	add("README.txt", []byte("Console diagnostics. tree-sanitized.frame is the device state with every string\n"+
		"replaced; replay it with: rcsim --fixture tree-sanitized.frame\n"))
	if err := z.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
