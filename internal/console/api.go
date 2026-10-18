package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// register adds the API routes. Reads are GET; every write is a POST that
// needs the CSRF header (sdk.CSRF) and, if configured, the PIN.
func (c *Console) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", c.handleStatus)
	mux.HandleFunc("GET /api/tree", c.handleTree)
	mux.HandleFunc("GET /api/summary", c.handleSummary)
	mux.HandleFunc("GET /api/events", c.handleEvents)
	mux.HandleFunc("GET /api/clients", c.handleClients)
	mux.HandleFunc("GET /api/audit", c.handleAudit)
	mux.HandleFunc("GET /api/diagnostics", c.handleDiagnostics)
	mux.HandleFunc("GET /api/hw", c.handleHW)
	mux.HandleFunc("POST /api/set", c.withPIN(c.handleSet))
	mux.HandleFunc("POST /api/press", c.withPIN(c.handlePress))
	mux.HandleFunc("POST /api/trigger", c.withPIN(c.handleTrigger))
	mux.HandleFunc("POST /api/link", c.withPIN(c.handleLink))
	mux.HandleFunc("POST /api/record", c.withPIN(c.handleRecord))
	mux.HandleFunc("POST /api/control", c.forPeople(c.withPIN(c.handleControl)))
	mux.HandleFunc("POST /api/settings", c.forPeople(c.withPIN(c.handleSettings)))
	mux.HandleFunc("POST /api/reconnect", c.forPeople(c.withPIN(c.handleReconnect)))
	mux.HandleFunc("POST /api/resync", c.forPeople(c.withPIN(c.handleResync)))
	mux.HandleFunc("POST /api/pin", c.forPeople(c.handlePIN))            // verifies the current PIN itself
	mux.HandleFunc("POST /api/pin/reset", c.forPeople(c.handlePinReset)) // proven at the device, see pinreset.go
	mux.HandleFunc("POST /api/presence", c.handlePresence)               // for rcplugd only, see pinreset.go
	// Everything else under api/ is an error in the API's own format.
	notFound := func(w http.ResponseWriter, r *http.Request) {
		sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "not_found", "no such API endpoint (or wrong method): "+r.Method+" "+r.URL.Path))
	}
	mux.HandleFunc("GET /api/", notFound)
	mux.HandleFunc("POST /api/", notFound)
}

func (c *Console) withPIN(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.who(r).plugin != "" {
			h(w, r) // plugins: their permission instead of the PIN (who.go)
			return
		}
		if err := c.pin.check(r); err != nil {
			c.auditAction(r, strings.TrimPrefix(r.URL.Path, "/api/"), "", err)
			sdk.WriteError(w, err)
			return
		}
		h(w, r)
	}
}

func (c *Console) handleStatus(w http.ResponseWriter, r *http.Request) {
	sdk.WriteJSON(w, http.StatusOK, c.statusDoc())
}

// handleTree returns the whole (redacted) tree plus layout, names and the
// exact list of properties writable at the current level.
func (c *Console) handleTree(w http.ResponseWriter, r *http.Request) {
	level, limit := c.Level()
	// Copy under the mirror lock (which the desk's read loop needs), encode
	// outside it.
	var s desk.Snapshot
	c.mirror.View(func(snap desk.Snapshot) {
		s = snap
		if snap.Root != nil {
			s.Root = snap.Root.Clone()
		}
	})
	if s.Root == nil {
		sdk.WriteError(w, desk.ErrNotSynced)
		return
	}
	caps := Capabilities(level, s.Root)
	if c.client.Passive() {
		// Reading along cannot write to the mixer; with the device buttons
		// switched on, mute and listen of the physical channels are pressed
		// there (the level still counts).
		w := map[string][]string{}
		if c.panelOn() {
			if all, ok := caps["writable"].(map[string][]string); ok {
				for typ, props := range panelProps {
					for _, p := range all[typ] {
						if _, ok := props[p]; ok {
							w[typ] = append(w[typ], p)
						}
					}
				}
			}
		}
		caps["writable"] = w
	}
	meta, _ := json.Marshal(map[string]interface{}{
		"passive":      c.client.Passive(),
		"layout":       s.Layout,
		"sources":      rcproto.Sources,
		"outputs":      rcproto.Outputs,
		"faders":       s.Layout.Faders(),
		"control":      caps,
		"controlLimit": limit,
		"unityFader":   rcproto.UnityFader,
	})
	var b bytes.Buffer
	b.Grow(512 << 10)
	fmt.Fprintf(&b, `{"epoch":%d,"seq":%d,"stale":%t,"meta":`, s.Epoch, s.Seq, s.Stale)
	b.Write(meta)
	b.WriteString(`,"root":`)
	desk.WriteTree(&b, s.Root, Secret)
	b.WriteString(`}`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b.Bytes())
}

func (c *Console) handleSummary(w http.ResponseWriter, r *http.Request) {
	var out interface{}
	c.mirror.View(func(s desk.Snapshot) {
		if s.Root != nil {
			out = Summarize(s.Root, s.Layout, s.Stale)
		}
	})
	if out == nil {
		sdk.WriteError(w, desk.ErrNotSynced)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, out)
}

// handleEvents streams changes as server-sent events. The first event is
// "hello" with the status document (including seq): a client that loaded the
// tree with a lower seq reloads it.
func (c *Console) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, err := c.mirror.Subscribe()
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	defer c.mirror.Unsubscribe(sub)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	hello, _ := json.Marshal(c.statusDoc())
	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: %s\n\n", hello)
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case m := <-sub.C:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Event, m.Data); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// handleClients lists the clients connected to the desk's control port. The
// /proc scan is cached for 2 s so it cannot be used to load the device.
func (c *Console) handleClients(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if time.Since(c.clientsAt) > 2*time.Second {
		peers, err := desk.ClientsOf(c.client.Port())
		if err != nil {
			c.clientsDoc = map[string]interface{}{"port": c.client.Port(), "error": err.Error()}
		} else {
			c.clientsDoc = map[string]interface{}{"port": c.client.Port(), "clients": peers}
		}
		c.clientsAt = time.Now()
	}
	doc := c.clientsDoc
	c.mu.Unlock()
	sdk.WriteJSON(w, http.StatusOK, doc)
}

func (c *Console) handleAudit(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	out := append([]AuditEntry(nil), c.audit...)
	c.mu.Unlock()
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"writes": out})
}

// writeReq is the body of every write endpoint.
type writeReq struct {
	Epoch    uint64      `json:"epoch"`
	Path     []int       `json:"path"`
	Name     string      `json:"name"`
	Value    interface{} `json:"value"`
	Ms       int         `json:"ms"`
	Source   int         `json:"source"`
	Output   int         `json:"output"`
	Link     bool        `json:"link"`
	Level    string      `json:"level"`
	Panel    *bool       `json:"panel"`
	FullCtl  *bool       `json:"fullControl"`
	ReadOnly *bool       `json:"readOnly"`
	Current  string      `json:"current"`
	New      string      `json:"new"`
}

func errBadRequest(msg string) error { return sdk.NewError(http.StatusBadRequest, "bad_request", msg) }

func decode(w http.ResponseWriter, r *http.Request) (*writeReq, bool) {
	var req writeReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		sdk.WriteError(w, errBadRequest("invalid request body: "+err.Error()))
		return nil, false
	}
	if n, ok := req.Value.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			sdk.WriteError(w, errBadRequest("invalid number"))
			return nil, false
		}
		req.Value = f
	}
	return &req, true
}

// remoteOf names the requester for the audit log. Behind the host the
// plugin is reachable on loopback only, and the host sets X-Forwarded-For
// itself (incoming values are discarded), so it can be trusted there.
func remoteOf(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}

// write validates and sends one property change, recording it in the audit log.
// errUntestedModel refuses control on models without a verified profile.
var errUntestedModel = sdk.NewError(http.StatusForbidden, "untested_model",
	"control is not available for this model yet (start the console with --allow-untested-model to try it)")

func (c *Console) write(by who, epoch uint64, path []int, name string, value interface{}) (*rcproto.Change, error) {
	level, limit := c.Level() // outside the mirror lock taken by Write
	remote := by.label()
	if by.plugin != "" && limit == "" {
		level = LevelBasic // a plugin's permission, whatever level the browser has set
	}
	nodeType := "?"
	var ch *rcproto.Change
	var err error
	if by.plugin != "" && !by.can("desk:write") {
		err = errNoDeskWrite(by.plugin)
	} else if limit == "untested_model" {
		err = errUntestedModel
	} else if limit == "read_only" {
		err = desk.ErrReadOnly
	} else if c.client.Passive() || (name == "faderLevel" && c.client.ReadsAlong() && c.panelOn() && c.physicalFader(path)) {
		// Reading along, or a physical fader in full control: the front
		// panel sets it as the hardware does (whether the mixer follows a
		// value written over the control port for a fader the panel
		// reports is not known).
		ch, nodeType, err = c.writeByPanel(level, epoch, path, name, value)
	} else {
		ch, err = c.client.Write(epoch, func(s desk.Snapshot) (*rcproto.Change, error) {
			if n := s.Root.At(path); n != nil {
				nodeType = n.Type
			}
			v, err := CheckWrite(level, s, path, name, value)
			if err != nil {
				return nil, err
			}
			return &rcproto.Change{Type: rcproto.PropertyChanged, Path: append([]int(nil), path...), Name: name, Value: v}, nil
		})
	}
	if path == nil {
		path = []int{} // the audit log always has a path
	}
	if nodeType == "?" { // refused before the desk was asked (read-only, untested model)
		c.mirror.View(func(s desk.Snapshot) {
			if s.Root != nil {
				if n := s.Root.At(path); n != nil {
					nodeType = n.Type
				}
			}
		})
	}
	e := AuditEntry{Time: time.Now().Format("15:04:05"), Node: nodeType, Path: path, Name: name, Remote: remote}
	if ch != nil {
		e.Value = ch.Value.String()
	} else {
		e.Value = fmt.Sprint(value)
	}
	if err != nil {
		e.Error = err.Error()
		if ae, ok := err.(*sdk.APIError); ok {
			e.Code = ae.Code
		}
		c.opts.Logf("console: write %s%v.%s=%v refused: %v", nodeType, path, name, value, err)
	} else {
		c.opts.Logf("console: write %s%v.%s=%s (from %s)", nodeType, path, name, e.Value, remote)
	}
	c.appendAudit(e)
	return ch, err
}

// panelProps are the properties that, while reading along, are changed on the
// front panel: a channel's buttons, a physical fader's position, and the
// SMART pads of the bank the device shows.
var panelProps = map[string]map[string]string{
	"CHANNEL":   {"channelOutputMute": "mute", "channelCueEnable": "listen"},
	"FADER":     {"faderLevel": "fader"},
	"PADBUTTON": {"padButtonPressed": "pad"},
}

// writeByPanel handles a write while reading along: the console cannot write
// to the mixer, but mute and listen of a channel with physical buttons are
// pressed on the front panel, a SMART pad of the bank the device shows is
// pressed there, and a physical fader's position is set there (until the real
// fader moves; the policy allows it at the expert level only). The device reports the result, which the mirror then shows; nothing
// is applied here.
func (c *Console) writeByPanel(level Level, epoch uint64, path []int, name string, value interface{}) (*rcproto.Change, string, error) {
	nodeType, button := "?", ""
	var want, cur rcproto.Value
	var err error
	c.mirror.View(func(s desk.Snapshot) {
		if s.Root == nil {
			err = desk.ErrNotSynced
			return
		}
		if s.Epoch != epoch {
			err = desk.ErrStaleEpoch
			return
		}
		n := s.Root.At(path)
		if n == nil {
			err = errBadRequest("no node at this path")
			return
		}
		nodeType = n.Type
		i := -1 // the strip with a physical fader and buttons, or the pad
		physical := s.Layout.PhysicalFaders
		if n.Type == "PADBUTTON" {
			physical = s.Layout.PadsPerBank
		}
		for j := 0; j < physical; j++ {
			if slices.Equal(s.Layout.Path(n.Type, j), path) {
				i = j
			}
		}
		b, ok := panelProps[n.Type][name]
		if !ok || i < 0 {
			err = desk.ErrPassive
			return
		}
		if n.Type == "FADER" {
			// The policy never lets a physical fader be written over the
			// network (it is not motorised); on the panel it is set as
			// the hardware reports it, until the real fader moves.
			want, err = panelFader(level, n, value)
		} else {
			want, err = CheckWrite(level, s, path, name, value)
		}
		if err != nil {
			return
		}
		cur, _ = n.Get(name)
		button = fmt.Sprintf("%s%d", b, i+1)
	})
	if err != nil {
		return nil, nodeType, err
	}
	if !c.panelOn() {
		return nil, nodeType, errPanelOff
	}
	ch := &rcproto.Change{Type: rcproto.PropertyChanged, Path: append([]int(nil), path...), Name: name, Value: want}
	pos := 0
	switch {
	case nodeType == "FADER":
		pos = int(want.I) // a position, set as it is
	case nodeType == "PADBUTTON":
		if !want.Truth() {
			return ch, nodeType, nil // the press on the panel released the pad already
		}
	case cur.Truth() == want.Truth():
		return ch, nodeType, nil // already so: pressing would toggle it back
	}
	if err := c.opts.Panel.Do(button, pos); err != nil {
		return nil, nodeType, sdk.NewError(http.StatusServiceUnavailable, "panel_failed", "pressing "+button+" failed: "+err.Error())
	}
	return ch, nodeType, nil
}

// physicalFader reports whether path is the FADER node of a physical fader.
func (c *Console) physicalFader(path []int) (ok bool) {
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout != nil {
			i := s.Layout.Runs["FADER"].IndexOf(path)
			ok = i >= 0 && i < s.Layout.PhysicalFaders
		}
	})
	return
}

// panelFader checks a physical fader position for the front panel: expert
// level, a whole number within the fader's range.
func panelFader(level Level, n *rcproto.Node, value interface{}) (rcproto.Value, error) {
	if level != LevelExpert {
		return rcproto.Value{}, sdk.NewError(http.StatusForbidden, "expert_required", "setting a physical fader on the front panel requires the expert control level")
	}
	f, ok := value.(float64)
	if !ok || f != float64(int64(f)) {
		return rcproto.Value{}, errBadRequest("faderLevel must be a whole number")
	}
	v := rcproto.Int(int64(f))
	if err := intBounds(n, &v, "faderMin", "faderMax"); err != nil {
		return rcproto.Value{}, errBadRequest("faderLevel " + err.Error())
	}
	return v, nil
}

var errPanelOff = sdk.NewError(http.StatusForbidden, "panel_off", "reading along, the console changes the desk through the device buttons: switch them on on the System page")

func (c *Console) appendAudit(e AuditEntry) {
	c.mu.Lock()
	c.audit = append(c.audit, e)
	if len(c.audit) > 300 {
		c.audit = c.audit[len(c.audit)-300:]
	}
	c.mu.Unlock()
}

// auditAction records a change to the console itself (control level, screen
// mirror, PIN, reconnect, front panel), allowed or refused, like a write.
// value never holds a PIN.
func (c *Console) auditAction(r *http.Request, name, value string, err error) {
	e := AuditEntry{Time: time.Now().Format("15:04:05"), Node: "console", Path: []int{}, Name: name, Value: value, Remote: remoteOf(r)}
	if err != nil {
		e.Error = err.Error()
		if ae, ok := err.(*sdk.APIError); ok {
			e.Code = ae.Code
		}
	}
	c.appendAudit(e)
}

func (c *Console) handleSet(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	ch, err := c.write(c.who(r), req.Epoch, req.Path, req.Name, req.Value)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	var b bytes.Buffer
	desk.WriteValue(&b, ch.Value, false, false)
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "value": json.RawMessage(b.Bytes())})
}

// handlePress performs a momentary boolean press (true, wait, false), e.g. a
// SMART pad button.
func (c *Console) handlePress(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	ms := req.Ms
	if ms <= 0 || ms > 2000 {
		ms = 150
	}
	remote := c.who(r)
	down, err := c.write(remote, req.Epoch, req.Path, req.Name, true)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	nodeType := ""
	c.mirror.View(func(s desk.Snapshot) {
		if n := s.Root.At(down.Path); n != nil {
			nodeType = n.Type
		}
	})
	go func() {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		// Always try to release, even if the tree moved meanwhile — but only
		// on the same kind of node at the same path.
		var epoch uint64
		same := false
		c.mirror.View(func(s desk.Snapshot) {
			epoch = s.Epoch
			if n := s.Root.At(down.Path); n != nil && n.Type == nodeType {
				same = true
			}
		})
		if !same {
			c.opts.Logf("console: release of %s%v.%s skipped: tree changed", nodeType, down.Path, req.Name)
			return
		}
		_, _ = c.write(remote, epoch, down.Path, req.Name, false)
	}()
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTrigger fires a binary trigger property (e.g. mixLinkRequest).
func (c *Console) handleTrigger(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	if err := c.pulse(c.who(r), req.Epoch, req.Path, req.Name); err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// pulse sends a trigger press. JUCE only notifies listeners when a value
// changes, so if the property still holds a press (the desk never acked the
// previous one) a release is sent first.
func (c *Console) pulse(remote who, epoch uint64, path []int, name string) error {
	pressed := false
	c.mirror.View(func(s desk.Snapshot) {
		if n := s.Root.At(path); n != nil {
			v, _ := n.Get(name)
			pressed = v.Kind == rcproto.KindBinary && bytes.Equal(v.B, rcproto.TriggerPress)
		}
	})
	if pressed {
		if _, err := c.write(remote, epoch, path, name, "release"); err != nil {
			return err
		}
	}
	_, err := c.write(remote, epoch, path, name, "press")
	return err
}

// handleRecord drives the recorder transport (0 stop, 1 pause, 2 record).
// requestRecordState may still hold the value of an earlier request (e.g. if
// recording was then stopped on the desk); writing the same value again would
// not notify the desk, so the current state is re-requested first — a no-op
// for the desk that makes the real request a change.
func (c *Console) handleRecord(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	want, isNum := req.Value.(float64)
	if !isNum || want != math.Trunc(want) || want < 0 || want > 2 {
		sdk.WriteError(w, errBadRequest("value must be 0 (stop), 1 (pause) or 2 (record)"))
		return
	}
	var path []int
	var reqState, recState int64 = -1, -1
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout == nil {
			return
		}
		path = s.Layout.Path("RECORDER", 0)
		if n := s.Root.At(path); n != nil {
			v, _ := n.Get("requestRecordState")
			reqState = v.I
			v, _ = n.Get("recordState")
			recState = v.I
		}
	})
	if path == nil {
		sdk.WriteError(w, desk.ErrNotSynced)
		return
	}
	remote := c.who(r)
	if int64(want) == reqState && recState != reqState && recState >= 0 && recState <= 2 {
		if _, err := c.write(remote, req.Epoch, path, "requestRecordState", float64(recState)); err != nil {
			sdk.WriteError(w, err)
			return
		}
	}
	if _, err := c.write(remote, req.Epoch, path, "requestRecordState", want); err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleLink links or unlinks a routing cell with the device-validated
// sequence (link = enable, unmute, mixLinkRequest; unlink = mixUnlinkRequest).
func (c *Console) handleLink(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	var path []int
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout != nil {
			path = s.Layout.MixPath(req.Source, req.Output)
		}
	})
	if path == nil {
		sdk.WriteError(w, errBadRequest("unknown routing cell"))
		return
	}
	remote := c.who(r)
	var err error
	if req.Link {
		if _, err = c.write(remote, req.Epoch, path, "mixDisabled", false); err == nil {
			if _, err = c.write(remote, req.Epoch, path, "mixMute", false); err == nil {
				err = c.pulse(remote, req.Epoch, path, "mixLinkRequest")
			}
		}
	} else {
		err = c.pulse(remote, req.Epoch, path, "mixUnlinkRequest")
	}
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *Console) handleControl(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	lv, err := ParseLevel(req.Level)
	if err != nil {
		c.auditAction(r, "control", req.Level, err)
		sdk.WriteError(w, err)
		return
	}
	if lv != LevelOff {
		var refused error
		switch _, limit := c.Level(); limit {
		case "read_only":
			refused = desk.ErrReadOnly
		case "untested_model":
			refused = errUntestedModel
		}
		if refused != nil {
			c.auditAction(r, "control", lv.String(), refused)
			sdk.WriteError(w, refused)
			return
		}
	}
	c.mu.Lock()
	c.settings.Control = lv.String()
	c.saveSettingsLocked()
	c.mu.Unlock()
	c.opts.Logf("console: control level set to %s by %s", lv, remoteOf(r))
	c.auditAction(r, "control", lv.String(), nil)
	c.notifyControl()
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "control": lv.String()})
}

func (c *Console) notifyControl() {
	data, _ := json.Marshal(c.statusDoc())
	c.mirror.Notify("control", data)
}

// handleSettings switches write protection, the front-panel emulation and
// full control.
func (c *Console) handleSettings(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}

	if req.Panel != nil {
		if *req.Panel && c.opts.Panel == nil {
			err := sdk.NewError(http.StatusConflict, "panel_unavailable", "front-panel emulation is not available here")
			c.auditAction(r, "panel", "true", err)
			sdk.WriteError(w, err)
			return
		}
		c.mu.Lock()
		c.settings.Panel = *req.Panel
		c.saveSettingsLocked()
		c.mu.Unlock()
		c.opts.Logf("console: front panel %v by %s", *req.Panel, remoteOf(r))
		c.auditAction(r, "panel", strconv.FormatBool(*req.Panel), nil)
	}
	if req.ReadOnly != nil {
		if !*req.ReadOnly && c.client.ReadOnly() {
			err := sdk.NewError(http.StatusConflict, "read_only_forced", "the console was started with --read-only")
			c.auditAction(r, "read-only", "false", err)
			sdk.WriteError(w, err)
			return
		}
		c.mu.Lock()
		c.settings.ReadOnly = *req.ReadOnly
		c.saveSettingsLocked()
		c.mu.Unlock()
		c.opts.Logf("console: read-only %v by %s", *req.ReadOnly, remoteOf(r))
		c.auditAction(r, "read-only", strconv.FormatBool(*req.ReadOnly), nil)
	}
	if req.FullCtl != nil {
		on := *req.FullCtl
		var err error
		switch lv, _ := c.Level(); {
		case on && !c.client.ReadsAlong():
			err = sdk.NewError(http.StatusConflict, "full_control_unavailable", "full control is only possible on the device, where the console reads along with its screen")
		case on && lv != LevelExpert:
			err = sdk.NewError(http.StatusForbidden, "expert_required", "full control requires the expert control level")
		}
		if err != nil {
			c.auditAction(r, "full control", "true", err)
			sdk.WriteError(w, err)
			return
		}
		c.mu.Lock()
		c.settings.FullControl = on
		c.saveSettingsLocked()
		c.mu.Unlock()
		c.client.SetFullControl(on)
		c.opts.Logf("console: full control %v by %s", on, remoteOf(r))
		c.auditAction(r, "full control", strconv.FormatBool(on), nil)
	}

	c.notifyControl()
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePIN sets, changes or removes the write PIN: {"current","new"}.
func (c *Console) handlePIN(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	what := "set"
	switch {
	case req.New == "":
		what = "removed"
	case req.Current != "":
		what = "changed"
	}
	if err := c.pin.change(req.Current, req.New); err != nil {
		c.auditAction(r, "pin", what, err)
		sdk.WriteError(w, err)
		return
	}
	c.opts.Logf("console: PIN %s by %s", what, remoteOf(r))
	c.auditAction(r, "pin", what, nil)
	c.notifyControl()
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleReconnect forces a new session (also the override after a guard
// conflict). Rate-limited: each call can briefly displace the GUI on a
// single-client desk.
func (c *Console) handleReconnect(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	wait := 10*time.Second - time.Since(c.lastReconnect)
	if wait > 0 {
		c.mu.Unlock()
		err := sdk.NewError(http.StatusTooManyRequests, "rate_limited",
			"reconnect is limited to once per 10 s; try again in "+strconv.Itoa(int(wait.Seconds())+1)+" s")
		c.auditAction(r, "reconnect", "", err)
		sdk.WriteError(w, err)
		return
	}
	c.lastReconnect = time.Now()
	c.mu.Unlock()
	c.opts.Logf("console: reconnect requested by %s", remoteOf(r))
	c.auditAction(r, "reconnect", "", nil)
	c.client.Reconnect()
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleResync gets a fresh full sync while reading along: it restarts the
// device's screen app, whose new connection to the mixer carries the whole
// state. Rate-limited like a reconnect.
func (c *Console) handleResync(w http.ResponseWriter, r *http.Request) {
	if !c.client.Passive() {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "not_passive", "the console is connected to the mixer itself; use reconnect"))
		return
	}
	c.mu.Lock()
	wait := 10*time.Second - time.Since(c.lastReconnect)
	if wait > 0 {
		c.mu.Unlock()
		err := sdk.NewError(http.StatusTooManyRequests, "rate_limited",
			"reading the state again is limited to once per 10 s; try again in "+strconv.Itoa(int(wait.Seconds())+1)+" s")
		c.auditAction(r, "resync", "", err)
		sdk.WriteError(w, err)
		return
	}
	c.lastReconnect = time.Now()
	c.mu.Unlock()
	if err := restartScreen(); err != nil {
		e := sdk.NewError(http.StatusConflict, "resync_failed", err.Error())
		c.auditAction(r, "resync", "", e)
		sdk.WriteError(w, e)
		return
	}
	c.opts.Logf("console: device screen restarted to read the state again, by %s", remoteOf(r))
	c.auditAction(r, "resync", "", nil)
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// restartScreen is desk.RestartScreen; tests replace it.
var restartScreen = desk.RestartScreen

// --- device hardware the console uses

// handleHW reports the front-panel emulation and the meter memory.
func (c *Console) handleHW(w http.ResponseWriter, r *http.Request) {
	p := map[string]interface{}{"enabled": c.panelOn(), "forced": c.opts.PanelEnabled}
	if c.opts.Panel != nil {
		if err := c.opts.Panel.Available(); err != nil {
			p["error"] = err.Error()
		}
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"panel":  p,
		"meters": map[string]interface{}{"available": c.opts.Meters != nil, "live": c.metersLive()},
	})
}
