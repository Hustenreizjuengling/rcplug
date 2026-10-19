package console

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
		body := rec.Body.Bytes()
		switch name {
		case "summary.json":
			body = redactSummary(body)
		case "audit.json":
			body = redactAudit(body)
		}
		add(name, body)
	}
	add("changes.json", c.changesForDiagnostics())
	c.mu.Lock()
	settings := map[string]interface{}{"control": c.settings.Control, "pinSet": c.pin.set(),
		"readOnly": c.client.ReadOnly() || c.settings.ReadOnly, "allowUntested": c.opts.AllowUntested, "panel": c.panelOnLocked()}
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

// The bundle goes into public bug reports: the device's, the show's and the
// pads' names leave the summary, the written values that are text and the
// browsers' addresses leave the write log (plugins stay named).
func redactSummary(raw []byte) []byte {
	var s capi.Summary
	if json.Unmarshal(raw, &s) != nil {
		return []byte("{}")
	}
	hide := func(v *string) {
		if *v != "" {
			*v = "(text)"
		}
	}
	hide(&s.Name)
	hide(&s.Show)
	for i := range s.Pads {
		hide(&s.Pads[i].Name)
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	return out
}

func redactAudit(raw []byte) []byte {
	var doc struct {
		Writes []AuditEntry `json:"writes"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return []byte("{}")
	}
	for i := range doc.Writes {
		e := &doc.Writes[i]
		if e.Node != "console" { // the console's own actions log fixed words
			e.Value = plainValue(e.Value)
		}
		if e.Remote != "device" && !strings.HasPrefix(e.Remote, "plugin ") {
			e.Remote = "(browser)"
		}
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}

// plainValue keeps numbers, switches and nothing; text becomes "(text)".
func plainValue(v string) string {
	switch v {
	case "", "true", "false", "void", "<nil>":
		return v
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v
	}
	return "(text)"
}
