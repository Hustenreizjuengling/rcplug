package console

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/hw"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

func quiet(string, ...interface{}) {}

type rig struct {
	sim     *rcsim.Server
	client  *desk.Client
	console *Console
	http    *httptest.Server
	dir     string
}

func newRig(t *testing.T, opts rcsim.Options, mutate ...func(*Options)) *rig {
	t.Helper()
	opts.Logf = quiet
	sim, err := rcsim.NewDuo(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Close)
	client := desk.NewClient(desk.Config{Addr: sim.Addr(), BootGrace: time.Nanosecond, Logf: quiet}, desk.NewMirror())
	dir := t.TempDir()
	o := Options{Client: client, DataDir: dir, Logf: quiet, Screen: &hw.Screen{FB: "/nonexistent", Mode: "fb"}}
	for _, m := range mutate {
		m(&o)
	}
	c := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	go client.Run(ctx)
	go c.Run(ctx)
	hs := httptest.NewServer(sdk.CSRF(c.Handler()))
	t.Cleanup(func() { cancel(); hs.Close() })
	r := &rig{sim: sim, client: client, console: c, http: hs, dir: dir}
	deadline := time.Now().Add(5 * time.Second)
	for client.Status().State != desk.StateLive {
		if time.Now().After(deadline) {
			t.Fatalf("desk not live: %+v", client.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r
}

type resp struct {
	code int
	body map[string]interface{}
	raw  []byte
}

func (r *rig) do(t *testing.T, method, path string, body interface{}, headers ...string) resp {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, r.http.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost {
		req.Header.Set(sdk.APIHeader, "1")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	out := resp{code: res.StatusCode, raw: buf.Bytes()}
	_ = json.Unmarshal(buf.Bytes(), &out.body)
	return out
}

func (r *rig) post(t *testing.T, path string, body interface{}, headers ...string) resp {
	return r.do(t, http.MethodPost, path, body, headers...)
}

// pathOf finds the first node of a type in the console's mirror.
func (r *rig) pathOf(nodeType string) []int {
	var found []int
	r.client.Mirror().View(func(s desk.Snapshot) {
		var walk func(n *rcproto.Node, path []int) bool
		walk = func(n *rcproto.Node, path []int) bool {
			if n.Type == nodeType {
				found = append([]int{}, path...)
				return true
			}
			for i, c := range n.Children {
				if walk(c, append(path, i)) {
					return true
				}
			}
			return false
		}
		walk(s.Root, []int{})
	})
	return found
}

// recorder reads a RECORDER property from the console's mirror.
func (r *rig) recorder(name string) int64 {
	v := int64(-1)
	r.client.Mirror().View(func(s desk.Snapshot) {
		if s.Layout != nil {
			if n := s.Layout.Node(s.Root, "RECORDER", 0); n != nil {
				x, _ := n.Get(name)
				v = x.I
			}
		}
	})
	return v
}

// eventually waits up to 2 s for ok.
func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not reached within 2 s")
}

func (r *rig) snap() (l *rcproto.Layout, epoch uint64) {
	r.client.Mirror().View(func(s desk.Snapshot) { l, epoch = s.Layout, s.Epoch })
	return
}

func (r *rig) control(t *testing.T, level string) {
	t.Helper()
	if res := r.post(t, "/api/control", map[string]string{"level": level}); res.code != 200 {
		t.Fatalf("control %s: %d %s", level, res.code, res.raw)
	}
}

func TestTreeIsRedactedAndComplete(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	res := r.do(t, http.MethodGet, "/api/tree", nil)
	if res.code != 200 {
		t.Fatalf("tree %d", res.code)
	}
	s := string(res.raw)
	if strings.Contains(s, "geheim-1234") {
		t.Fatal("Wi-Fi key leaked through /api/tree")
	}
	if strings.Contains(s, `"parent":null`) {
		t.Fatal("layout runs must carry an array parent, not null")
	}
	var doc struct {
		Meta struct {
			Layout  rcproto.Layout `json:"layout"`
			Control struct {
				Level    string              `json:"level"`
				Writable map[string][]string `json:"writable"`
			} `json:"control"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(res.raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Meta.Layout.Model != rcproto.ModelDuo || doc.Meta.Layout.Runs["CHANNEL"].Count != 10 || !doc.Meta.Layout.Controllable {
		t.Fatalf("meta layout %+v", doc.Meta.Layout)
	}
	if doc.Meta.Control.Level != "off" || len(doc.Meta.Control.Writable) != 0 {
		t.Fatalf("read-only tree lists writable props: %+v", doc.Meta.Control)
	}
	r.control(t, "basic")
	res = r.do(t, http.MethodGet, "/api/tree", nil)
	_ = json.Unmarshal(res.raw, &doc)
	ch := strings.Join(doc.Meta.Control.Writable["CHANNEL"], ",")
	if !strings.Contains(ch, "channelOutputMute") || strings.Contains(strings.Join(doc.Meta.Control.Writable["NETWORK"], ","), "wifiPSK") {
		t.Fatalf("capabilities wrong: CHANNEL=%s", ch)
	}
}

func TestWritePolicy(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	l, epoch := r.snap()
	mute := map[string]interface{}{"epoch": epoch, "path": l.Path("CHANNEL", 0), "name": "channelOutputMute", "value": true}

	if res := r.post(t, "/api/set", mute); res.code != http.StatusForbidden || res.body["code"] != "control_off" {
		t.Fatalf("write in read-only mode: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/set", mute, sdk.APIHeader, ""); res.code != http.StatusForbidden || res.body["code"] != "csrf" {
		t.Fatalf("write without CSRF header: %d %s", res.code, res.raw)
	}
	r.control(t, "basic")
	if res := r.post(t, "/api/set", mute); res.code != 200 {
		t.Fatalf("mute: %d %s", res.code, res.raw)
	}
	in := l.Path("INPUTSOURCE", 0)
	cases := []struct {
		name string
		body map[string]interface{}
		code string // "" = success
	}{
		{"physical fader", map[string]interface{}{"path": l.Path("FADER", 0), "name": "faderLevel", "value": 50}, "out_of_range"},
		{"fader out of range", map[string]interface{}{"path": l.Path("FADER", 5), "name": "faderLevel", "value": 500}, "out_of_range"},
		{"virtual fader", map[string]interface{}{"path": l.Path("FADER", 5), "name": "faderLevel", "value": 100}, ""},
		{"unknown property", map[string]interface{}{"path": l.Path("CHANNEL", 0), "name": "noSuchThing", "value": true}, "unknown_property"},
		{"wrong type", map[string]interface{}{"path": l.Path("CHANNEL", 0), "name": "channelOutputMute", "value": 1}, "invalid_value"},
		{"numeric string", map[string]interface{}{"path": l.Path("RECORDER", 0), "name": "requestRecordState", "value": "2"}, "invalid_value"},
		{"firmware update", map[string]interface{}{"path": l.Path("SYSTEM", 0), "name": "updateInitiateRequested", "value": true}, "denied"},
		{"secret", map[string]interface{}{"path": l.Path("NETWORK", 0), "name": "wifiPSK", "value": "x"}, "denied"},
		{"stale epoch", map[string]interface{}{"epoch": epoch + 99, "path": l.Path("CHANNEL", 0), "name": "channelCueEnable", "value": true}, "stale_epoch"},
		{"mix level", map[string]interface{}{"path": l.MixPath(0, 4), "name": "mixLevelWithAnchor", "value": "0.5|0.5"}, ""},
		{"bad mix level", map[string]interface{}{"path": l.MixPath(0, 4), "name": "mixLevelWithAnchor", "value": "2|0"}, "out_of_range"},
		{"gain nudge", map[string]interface{}{"path": in, "name": "inputMicrophoneGain", "value": 45}, ""},
		{"gain jump", map[string]interface{}{"path": in, "name": "inputMicrophoneGain", "value": 100}, "out_of_range"},
		{"phantom 2", map[string]interface{}{"path": in, "name": "inputPower", "value": 2}, "out_of_range"},
		{"mic type (expert only)", map[string]interface{}{"path": in, "name": "inputMicrophoneType", "value": 1}, "denied"},
		{"delay too long", map[string]interface{}{"path": l.Path("MASTERCHANNEL", 0), "name": "masterDelaySeconds", "value": 9}, "out_of_range"},
	}
	for _, c := range cases {
		if _, ok := c.body["epoch"]; !ok {
			c.body["epoch"] = epoch
		}
		res := r.post(t, "/api/set", c.body)
		if c.code == "" && res.code != 200 {
			t.Errorf("%s: %d %s", c.name, res.code, res.raw)
		}
		if c.code != "" && res.body["code"] != c.code {
			t.Errorf("%s: got %d %v, want code %s", c.name, res.code, res.body["code"], c.code)
		}
	}
	r.control(t, "expert")
	for _, c := range []struct{ node, prop string }{
		{"SYSTEM", "updateRebootRequested"}, {"SYSTEM", "powerOffRequest"}, {"SYSTEM", "disableAllPhysicalButtons"},
		{"PLAYER", "playerFilePath"},
	} {
		v := interface{}(true)
		if strings.HasSuffix(c.prop, "Path") {
			v = "/etc/shadow"
		}
		if res := r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": l.Path(c.node, 0), "name": c.prop, "value": v}); res.body["code"] != "denied" {
			t.Errorf("expert %s.%s: %d %s", c.node, c.prop, res.code, res.raw)
		}
	}
	if res := r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": l.Path("FADER", 0), "name": "faderLevel", "value": 60}); res.code != 200 {
		t.Errorf("expert physical fader: %d %s", res.code, res.raw)
	}
	for _, c := range []struct {
		node, prop string
		value      interface{}
		code       string
	}{
		{"CURRENTSHOW", "currentShowName", "x", "denied"},
		{"SIPREGISTRATION", "sipRegistrationDetails", "x", "denied"},
		{"STREAMERXMIXPRESET", "streamerXPresetCreated", true, "denied"},
		{"PAD", "padName", "a\x00b", "invalid_value"},
		// Readings only the desk writes, node limits and identities.
		{"RECORDER", "recordState", 2, "denied"},
		{"RECORDER", "recordTimeMs", 5, "denied"},
		{"OUTPUT", "outputMonAutoMuteActive", true, "denied"},
		{"FADER", "faderMax", 1000, "denied"},
		{"POT", "potMin", 0, "denied"},
		{"INPUTSOURCE", "inputWirelessSN", "SN-1", "denied"},
		{"PAD", "padProgress", 1, "denied"},
	} {
		path := r.pathOf(c.node)
		if path == nil {
			t.Errorf("no %s node in the fixture", c.node)
			continue
		}
		if res := r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": path, "name": c.prop, "value": c.value}); res.body["code"] != c.code {
			t.Errorf("expert %s.%s: %d %s, want %s", c.node, c.prop, res.code, res.raw, c.code)
		}
	}
	if created := r.sim.Created(); len(created) != 0 {
		t.Fatalf("client created properties on the desk: %v", created)
	}
	raw, _ := os.ReadFile(filepath.Join(r.dir, "settings.json"))
	if !strings.Contains(string(raw), "expert") {
		t.Errorf("settings not persisted: %s", raw)
	}
	if res := r.do(t, http.MethodGet, "/api/audit", nil); !strings.Contains(string(res.raw), "channelOutputMute") {
		t.Errorf("audit log missing writes: %s", res.raw)
	}
}

func TestPIN(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	if res := r.post(t, "/api/pin", map[string]string{"current": "", "new": "4711"}); res.code != 200 {
		t.Fatalf("set pin: %d %s", res.code, res.raw)
	}
	if fi, err := os.Stat(filepath.Join(r.dir, "pin")); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("pin file: %v %v", fi, err)
	}
	if res := r.post(t, "/api/control", map[string]string{"level": "basic"}); res.code != 401 || res.body["code"] != "pin_required" {
		t.Fatalf("control without pin: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/control", map[string]string{"level": "basic"}, "X-Rcplug-Pin", "4711"); res.code != 200 {
		t.Fatalf("control with pin: %d %s", res.code, res.raw)
	}
	if res := r.do(t, http.MethodGet, "/api/status", nil); res.body["pinRequired"] != true {
		t.Fatalf("status pinRequired: %s", res.raw)
	}
	for i := 0; i < pinMaxFailures; i++ {
		r.post(t, "/api/reconnect", nil, "X-Rcplug-Pin", "0000")
	}
	if res := r.post(t, "/api/control", map[string]string{"level": "off"}, "X-Rcplug-Pin", "4711"); res.code != 429 {
		t.Fatalf("lockout not enforced: %d %s", res.code, res.raw)
	}
	// A restart reads the PIN from the file.
	if p := newPinStore(r.dir); !p.set() || p.verify("4711") != nil {
		t.Fatal("pin not persisted")
	}
}

func TestReconnectRateLimited(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	if res := r.post(t, "/api/reconnect", nil); res.code != 200 {
		t.Fatalf("first reconnect: %d", res.code)
	}
	if res := r.post(t, "/api/reconnect", nil); res.code != 429 {
		t.Fatalf("second reconnect: %d %s", res.code, res.raw)
	}
}

func TestRecordValidatesAndHandlesRepeatedRequests(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	_, epoch := r.snap()
	for _, bad := range []interface{}{"2", nil, 3, 1.5} {
		if res := r.post(t, "/api/record", map[string]interface{}{"epoch": epoch, "value": bad}); res.code != 400 {
			t.Errorf("record %v: %d %s", bad, res.code, res.raw)
		}
	}
	if res := r.post(t, "/api/record", map[string]interface{}{"epoch": epoch, "value": 2}); res.code != 200 {
		t.Fatalf("record: %d %s", res.code, res.raw)
	}
	// Recording is stopped on the desk itself: request stays 2, state goes 0.
	// Wait for the simulator to have reacted to the first request, otherwise
	// its reaction (state 2) could overwrite the injected stop.
	l, _ := r.snap()
	eventually(t, func() bool { return r.recorder("recordState") == 2 })
	n := len(r.sim.Writes())
	_ = r.sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: l.Path("RECORDER", 0), Name: "recordState", Value: rcproto.Int(0)})
	eventually(t, func() bool { return r.recorder("recordState") == 0 })
	if res := r.post(t, "/api/record", map[string]interface{}{"epoch": epoch, "value": 2}); res.code != 200 {
		t.Fatalf("record again: %d %s", res.code, res.raw)
	}
	// The simulator reads client writes asynchronously.
	var w []rcproto.Change
	eventually(t, func() bool { w = r.sim.Writes()[n:]; return len(w) >= 2 })
	if len(w) != 2 || w[0].Value.I != 0 || w[1].Value.I != 2 {
		t.Fatalf("expected re-request 0 then 2, got %+v", w)
	}
}

func TestLinkPressAndTrigger(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	l, epoch := r.snap()
	if res := r.post(t, "/api/link", map[string]interface{}{"epoch": epoch, "source": 0, "output": 4, "link": false}); res.code != 200 {
		t.Fatalf("unlink %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/press", map[string]interface{}{"epoch": epoch, "path": l.Path("PADBUTTON", 0), "name": "padButtonPressed", "ms": 30}); res.code != 200 {
		t.Fatalf("press %d %s", res.code, res.raw)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var unlink, downs, ups int
		for _, w := range r.sim.Writes() {
			switch {
			case w.Name == "mixUnlinkRequest" && bytes.Equal(w.Value.B, rcproto.TriggerPress):
				unlink++
			case w.Name == "padButtonPressed" && w.Value.Truth():
				downs++
			case w.Name == "padButtonPressed":
				ups++
			}
		}
		if unlink == 1 && downs == 1 && ups == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unlink=%d press=%d release=%d", unlink, downs, ups)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScreenMirrorIsOptIn(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	if res := r.do(t, http.MethodGet, "/api/screen.jpg", nil); res.code != 403 || res.body["code"] != "screen_disabled" {
		t.Fatalf("screen without opt-in: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]bool{"screen": true}); res.code != 200 {
		t.Fatalf("enable screen: %d %s", res.code, res.raw)
	}
	// Enabled, but the device has no framebuffer here: a capture error, not 403.
	if res := r.do(t, http.MethodGet, "/api/screen.jpg", nil); res.code != 503 || res.body["code"] != "capture_failed" {
		t.Fatalf("screen enabled: %d %s", res.code, res.raw)
	}
	if res := r.do(t, http.MethodGet, "/api/screen.mjpg", nil); res.code != 503 || res.body["code"] != "capture_failed" {
		t.Fatalf("screen enabled: %d %s", res.code, res.raw)
	}
}

// The front panel is switched on in the UI (no --panel needed) and stays on
// across a restart; the picture's rotation is a setting as well.
func TestPanelAndScreenRotationAreSettings(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Panel = &hw.Panel{TTY: "/dev/null"} })
	if res := r.post(t, "/api/control", map[string]string{"level": "expert"}); res.code != 200 {
		t.Fatalf("expert: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/panel", map[string]string{"control": "listen1"}); res.code != 403 || res.body["code"] != "panel_disabled" {
		t.Fatalf("panel before switching it on: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]bool{"panel": true}); res.code != 200 {
		t.Fatalf("switch panel on: %d %s", res.code, res.raw)
	}
	hwDoc := r.do(t, http.MethodGet, "/api/hw", nil).body["panel"].(map[string]interface{})
	if hwDoc["enabled"] != true || hwDoc["forced"] != false {
		t.Fatalf("hw panel: %v", hwDoc)
	}
	// Switched on: the press reaches the tty (/dev/null refuses TIOCSTI).
	if res := r.post(t, "/api/panel", map[string]string{"control": "listen1"}); res.code != 400 || !strings.Contains(string(res.raw), "TIOCSTI") {
		t.Fatalf("panel when on: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]string{"screenRotate": "90"}); res.code != 200 {
		t.Fatalf("rotate: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]string{"screenRotate": "45"}); res.code != 400 {
		t.Fatalf("bad rotation accepted: %d %s", res.code, res.raw)
	}
	var saved Settings
	if err := sdk.LoadJSON(filepath.Join(r.dir, "settings.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Panel || saved.ScreenRotate != "90" {
		t.Fatalf("saved %+v", saved)
	}
	again := New(Options{Client: r.client, DataDir: r.dir, Logf: quiet, Panel: &hw.Panel{TTY: "/dev/null"}})
	if !again.panelOn() {
		t.Fatal("the panel switch did not survive a restart")
	}
	if res := r.post(t, "/api/settings", map[string]interface{}{"panel": false, "screenRotate": "auto"}); res.code != 200 {
		t.Fatalf("switch off: %d %s", res.code, res.raw)
	}
	if r.console.panelOn() || r.console.settings.ScreenRotate != "" {
		t.Fatalf("after switching off: panel %v, rotate %q", r.console.panelOn(), r.console.settings.ScreenRotate)
	}
}

// Reading along: the status says so, and "read the state again" restarts
// the device's screen (at most every 10 s); connected, there is nothing to
// restart.
func TestResyncRestartsTheScreenWhileReadingAlong(t *testing.T) {
	calls := 0
	old := restartScreen
	restartScreen = func() error { calls++; return nil }
	defer func() { restartScreen = old }()
	client := desk.NewClient(desk.Config{Addr: "127.0.0.1:2345", Mode: "tap", Logf: quiet}, desk.NewMirror())
	hs := httptest.NewServer(sdk.CSRF(New(Options{Client: client, Logf: quiet}).Handler()))
	defer hs.Close()
	r := &rig{http: hs}
	if st := r.do(t, http.MethodGet, "/api/status", nil); st.body["passive"] != true {
		t.Fatalf("status: %s", st.raw)
	}
	if res := r.post(t, "/api/resync", map[string]string{}); res.code != 200 || calls != 1 {
		t.Fatalf("resync: %d %s (calls %d)", res.code, res.raw, calls)
	}
	if res := r.post(t, "/api/resync", map[string]string{}); res.code != 429 || calls != 1 {
		t.Fatalf("second resync: %d %s (calls %d)", res.code, res.raw, calls)
	}

	connected := newRig(t, rcsim.Options{})
	if res := connected.post(t, "/api/resync", map[string]string{}); res.code != 409 || res.body["code"] != "not_passive" {
		t.Fatalf("resync while connected: %d %s", res.code, res.raw)
	}
	if st := connected.do(t, http.MethodGet, "/api/status", nil); st.body["passive"] != false {
		t.Fatalf("connected status: %s", st.raw)
	}
}

type fakePanel struct {
	pressed []string
	values  []int
}

func (f *fakePanel) Do(id string, value int) error {
	f.pressed = append(f.pressed, id)
	f.values = append(f.values, value)
	return nil
}
func (f *fakePanel) Available() error { return nil }

// Reading along, mute and listen of the physical channels are pressed on the
// front panel (when the device buttons are switched on); everything else is
// refused. The mirror is not changed: the device reports the result.
func TestReadingAlongWritesThroughTheFrontPanel(t *testing.T) {
	root, err := rcsim.DuoTree()
	if err != nil {
		t.Fatal(err)
	}
	mirror := desk.NewMirror()
	if err := mirror.Ingest(&rcproto.Change{Type: rcproto.FullSync, Tree: root}); err != nil {
		t.Fatal(err)
	}
	fp := &fakePanel{}
	client := desk.NewClient(desk.Config{Addr: "127.0.0.1:2345", Mode: "tap", Logf: quiet}, mirror)
	c := New(Options{Client: client, Logf: quiet, Panel: fp, DataDir: t.TempDir()})
	hs := httptest.NewServer(sdk.CSRF(c.Handler()))
	defer hs.Close()
	r := &rig{http: hs, console: c}
	if res := r.post(t, "/api/control", map[string]string{"level": "basic"}); res.code != 200 {
		t.Fatalf("level: %d %s", res.code, res.raw)
	}
	var epoch uint64
	var ch0, ch5 []int
	var muted, cued bool
	mirror.View(func(s desk.Snapshot) {
		epoch, ch0, ch5 = s.Epoch, s.Layout.Path("CHANNEL", 0), s.Layout.Path("CHANNEL", 5)
		v, _ := s.Root.At(ch0).Get("channelOutputMute")
		w, _ := s.Root.At(ch0).Get("channelCueEnable")
		muted, cued = v.Truth(), w.Truth()
	})
	set := func(path []int, name string, v interface{}) resp {
		return r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": path, "name": name, "value": v})
	}
	if res := set(ch0, "channelOutputMute", !muted); res.code != 403 || res.body["code"] != "panel_off" {
		t.Fatalf("device buttons off: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]bool{"panel": true}); res.code != 200 {
		t.Fatalf("panel on: %d %s", res.code, res.raw)
	}
	tree := r.do(t, http.MethodGet, "/api/tree", nil)
	meta := tree.body["meta"].(map[string]interface{})
	// Only what the panel can do: the channel buttons and (where the level
	// allows faders) the fader position.
	writable := meta["control"].(map[string]interface{})["writable"].(map[string]interface{})
	for typ, props := range writable {
		for _, p := range props.([]interface{}) {
			if _, ok := panelProps[typ][p.(string)]; !ok {
				t.Fatalf("writable while reading along: %s.%s", typ, p)
			}
		}
	}
	if len(writable["CHANNEL"].([]interface{})) != 2 {
		t.Fatalf("writable while reading along: %v", writable)
	}
	if res := set(ch0, "channelOutputMute", !muted); res.code != 200 {
		t.Fatalf("mute: %d %s", res.code, res.raw)
	}
	if res := set(ch0, "channelOutputMute", muted); res.code != 200 { // already so in the mirror
		t.Fatalf("mute as it is: %d %s", res.code, res.raw)
	}
	if res := set(ch0, "channelCueEnable", !cued); res.code != 200 {
		t.Fatalf("listen: %d %s", res.code, res.raw)
	}
	if strings.Join(fp.pressed, ",") != "mute1,listen1" {
		t.Fatalf("pressed %v", fp.pressed)
	}
	if res := set(ch5, "channelOutputMute", true); res.code != 403 || res.body["code"] != "passive" {
		t.Fatalf("virtual channel: %d %s", res.code, res.raw)
	}
	if res := set(ch0, "channelPanOn", true); res.code != 403 || res.body["code"] != "passive" {
		t.Fatalf("other property: %d %s", res.code, res.raw)
	}
	muted2 := false
	mirror.View(func(s desk.Snapshot) { v, _ := s.Root.At(ch0).Get("channelOutputMute"); muted2 = v.Truth() })
	if muted2 != muted {
		t.Fatal("the mirror was changed without the device reporting it")
	}

	// A physical fader's position is set on the panel, at the expert level only.
	var fd0, fd5 []int
	mirror.View(func(s desk.Snapshot) { fd0, fd5 = s.Layout.Path("FADER", 0), s.Layout.Path("FADER", 5) })
	if res := set(fd0, "faderLevel", 30); res.code != 403 {
		t.Fatalf("physical fader at basic: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/control", map[string]string{"level": "expert"}); res.code != 200 {
		t.Fatalf("expert: %d %s", res.code, res.raw)
	}
	if res := set(fd0, "faderLevel", 30); res.code != 200 {
		t.Fatalf("physical fader at expert: %d %s", res.code, res.raw)
	}
	if res := set(fd5, "faderLevel", 30); res.code != 403 || res.body["code"] != "passive" {
		t.Fatalf("virtual fader: %d %s", res.code, res.raw)
	}
	if got := strings.Join(fp.pressed, ","); got != "mute1,listen1,fader1" || fp.values[2] != 30 {
		t.Fatalf("pressed %v, values %v", got, fp.values)
	}
}

type fakeTouch struct{ gestures []hw.Gesture }

func (f *fakeTouch) Info() (hw.TouchInfo, error) {
	return hw.TouchInfo{Device: "/dev/input/event0", Name: "Goodix Capacitive TouchScreen", XMax: 719, YMax: 1279, MT: true}, nil
}
func (f *fakeTouch) Do(g hw.Gesture) error { f.gestures = append(f.gestures, g); return nil }

// Clicks on the mirror tap the touchscreen where the device shows that spot:
// only switched on, at the expert level, one gesture at a time.
func TestTouchOnTheMirror(t *testing.T) {
	ft := &fakeTouch{}
	r := newRig(t, rcsim.Options{}, func(o *Options) {
		o.Screen = hw.StillScreen(image.NewRGBA(image.Rect(0, 0, 720, 1280))) // like the Duo: portrait, shown turned
		o.Touch = ft
	})
	tap := map[string]float64{"x": 0.1, "y": 0.1}
	if res := r.post(t, "/api/touch", tap); res.code != 403 || res.body["code"] != "screen_disabled" {
		t.Fatalf("mirror off: %d %s", res.code, res.raw)
	}
	r.post(t, "/api/settings", map[string]bool{"screen": true})
	if res := r.post(t, "/api/touch", tap); res.code != 403 || res.body["code"] != "touch_disabled" {
		t.Fatalf("screen control off: %d %s", res.code, res.raw)
	}
	r.post(t, "/api/settings", map[string]bool{"screenControl": true})
	if res := r.post(t, "/api/touch", tap); res.code != 403 || res.body["code"] != "expert_required" {
		t.Fatalf("not expert: %d %s", res.code, res.raw)
	}
	r.post(t, "/api/control", map[string]string{"level": "expert"})
	old := touchInterval
	defer func() { touchInterval = old }()
	touchInterval = time.Hour // the next gesture is always "too soon"
	if res := r.post(t, "/api/touch", tap); res.code != 200 {
		t.Fatalf("tap: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/touch", tap); res.code != 429 {
		t.Fatalf("two at once: %d %s", res.code, res.raw)
	}
	touchInterval = 0
	swipe := map[string]float64{"x": 0.1, "y": 0.5, "toX": 0.9, "toY": 0.5, "ms": 300}
	if res := r.post(t, "/api/touch", swipe); res.code != 200 {
		t.Fatalf("swipe: %d %s", res.code, res.raw)
	}
	if len(ft.gestures) != 2 {
		t.Fatalf("gestures %+v", ft.gestures)
	}
	g := ft.gestures[0]
	if len(g.Points) != 1 || !g.Portrait || math.Abs(g.Points[0].X-0.9) > 1e-9 || math.Abs(g.Points[0].Y-0.1) > 1e-9 {
		t.Fatalf("tap at the top left of the picture: %+v (want the top right of the portrait buffer)", g)
	}
	if s := ft.gestures[1]; len(s.Points) != 2 || s.Duration != 300*time.Millisecond {
		t.Fatalf("swipe %+v", s)
	}
	doc := r.do(t, http.MethodGet, "/api/hw", nil).body["touch"].(map[string]interface{})
	if doc["available"] != true || doc["enabled"] != true || doc["device"] != "/dev/input/event0" {
		t.Fatalf("hw touch %v", doc)
	}
	if res := r.post(t, "/api/touch", map[string]float64{"x": 1.5, "y": 0}); res.code != 400 {
		t.Fatalf("outside the picture: %d %s", res.code, res.raw)
	}
}

func TestUntestedModelIsReadOnly(t *testing.T) {
	root, _ := rcsim.DuoTree()
	rcsim.MakeRealistic(root)
	for _, c := range root.Children {
		if c.Type == "SYSTEM" {
			c.Set("boardType", rcproto.Int(0)) // Pro II: no verified profile
		}
	}
	sim := rcsim.New(root, rcsim.Options{Logf: quiet})
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	client := desk.NewClient(desk.Config{Addr: sim.Addr(), BootGrace: time.Nanosecond, Logf: quiet}, desk.NewMirror())
	c := New(Options{Client: client, Logf: quiet})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	for client.Status().State != desk.StateLive {
		time.Sleep(10 * time.Millisecond)
	}
	c.settings.Control = "basic"
	if lv, why := c.Level(); lv != LevelOff || why != "untested_model" {
		t.Fatalf("level %s (%s)", lv, why)
	}
	// The API says why instead of a generic "control is off".
	srv := httptest.NewServer(c.Handler())
	defer srv.Close()
	for path, body := range map[string]string{
		"/api/control": `{"level":"basic"}`,
		"/api/set":     `{"epoch":1,"path":[28],"name":"channelOutputMute","value":true}`,
	} {
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden || !strings.Contains(string(raw), `"untested_model"`) {
			t.Errorf("%s: %d %s, want 403 untested_model", path, res.StatusCode, raw)
		}
	}
	c.opts.AllowUntested = true
	if lv, _ := c.Level(); lv != LevelBasic {
		t.Fatalf("override ignored: %s", lv)
	}
}

func TestEventsStream(t *testing.T) {
	r := newRig(t, rcsim.Options{MeterHz: 50})
	req, _ := http.NewRequest(http.MethodGet, r.http.URL+"/api/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 64<<10)
	var got strings.Builder
	for !strings.Contains(got.String(), "event: changes") {
		n, err := res.Body.Read(buf)
		if err != nil {
			t.Fatalf("stream ended: %v (%q)", err, got.String())
		}
		got.Write(buf[:n])
	}
	s := got.String()
	if !strings.Contains(s, "event: hello") || !strings.Contains(s, `"seq":`) || !strings.Contains(s, "meterLevelL") {
		t.Fatalf("unexpected stream: %.300s", s)
	}
}

func TestSummary(t *testing.T) {
	root, _ := rcsim.DuoTree()
	rcsim.MakeRealistic(root)
	s := Summarize(root, rcproto.DiscoverLayout(root), false)
	if len(s.Strips) != 9 || s.Strips[0].SourceID != "combo1" || s.Strips[0].SourceLabel != "Mic 1" || !s.Strips[3].Muted || s.Strips[4].Physical {
		t.Fatalf("strips %+v", s.Strips)
	}
	if s.Strips[0].Fader != 90 || s.Strips[0].FaderDb != 0 {
		t.Fatalf("fader %d %.2f", s.Strips[0].Fader, s.Strips[0].FaderDb)
	}
	if len(s.Pads) != 21 || s.Pads[7].Bank != 1 || s.Pads[7].Slot != 1 {
		t.Fatalf("pads %+v", s.Pads[:8])
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

// The public SDK client works against the real console API.
func TestSDKClient(t *testing.T) {
	r := newRig(t, rcsim.Options{MeterHz: 20})
	cl := capi.New("")
	cl.Base = r.http.URL + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := cl.Status(ctx)
	if err != nil || st.Connection.State != "live" || st.Model != "duo" || !st.Supported {
		t.Fatalf("status %+v %v", st, err)
	}
	sum, err := cl.Summary(ctx)
	if err != nil || len(sum.Strips) != 9 {
		t.Fatalf("summary %v", err)
	}
	if raw, err := cl.Tree(ctx); err != nil || !bytes.Contains(raw, []byte(`"root"`)) {
		t.Fatalf("tree %v", err)
	}
	l, _ := r.snap()
	err = cl.Set(ctx, st.Epoch, l.Path("CHANNEL", 1), "channelCueEnable", true)
	if ce, ok := err.(*capi.Error); !ok || ce.Code != "control_off" {
		t.Fatalf("set while off: %v", err)
	}
	r.control(t, "basic")
	if err := cl.Set(ctx, st.Epoch, l.Path("CHANNEL", 1), "channelCueEnable", true); err != nil {
		t.Fatal(err)
	}
	ectx, ecancel := context.WithCancel(ctx)
	got := map[string]bool{}
	_ = cl.Events(ectx, func(e capi.Event) {
		got[e.Name] = true
		if got["hello"] && got["changes"] {
			ecancel()
		}
	})
	if !got["hello"] || !got["changes"] {
		t.Fatalf("events %v", got)
	}
}

func TestPINRules(t *testing.T) {
	p := newPinStore(t.TempDir())
	if ok, err := p.adopt("2468"); !ok || err != nil {
		t.Fatalf("adopt: %v %v", ok, err)
	}
	if ok, _ := p.adopt("9999"); ok || p.verify("2468") != nil {
		t.Fatal("adopt must not replace an existing PIN")
	}
	// Requests that carry no PIN at all are not guesses.
	for i := 0; i < 3*pinMaxFailures; i++ {
		if err := p.verify(""); err != errPin {
			t.Fatalf("verify(\"\") = %v", err)
		}
	}
	if err := p.verify("2468"); err != nil {
		t.Fatalf("locked out by PIN-less requests: %v", err)
	}
	for _, bad := range []string{"Müller", "a\tb", strings.Repeat("1", 65)} {
		if err := p.change("2468", bad); err == nil {
			t.Errorf("PIN %q accepted", bad)
		}
	}
	if err := p.change("2468", "open sesame!"); err != nil {
		t.Errorf("printable ASCII PIN refused: %v", err)
	}
}

func TestUnknownEndpointsAnswerInJSON(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	for _, c := range []struct{ method, path string }{{http.MethodGet, "/api/nope"}, {http.MethodGet, "/api/set"}, {http.MethodPost, "/api/status"}} {
		if res := r.do(t, c.method, c.path, nil, "X-Rcplug", "1"); res.code != 404 || res.body["code"] != "not_found" {
			t.Errorf("%s %s: %d %s", c.method, c.path, res.code, res.raw)
		}
	}
}

// Changes to the console itself are in the audit log too; PINs never are.
func TestAuditListsConsoleActions(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	if res := r.post(t, "/api/pin", map[string]string{"new": "pin-4711-x"}); res.code != 200 {
		t.Fatalf("set PIN: %d %s", res.code, res.raw)
	}
	r.post(t, "/api/control", map[string]string{"level": "expert"}) // without the PIN: refused
	res := r.do(t, http.MethodGet, "/api/audit", nil)
	if strings.Contains(string(res.raw), "pin-4711-x") {
		t.Fatalf("the PIN is in the audit log: %s", res.raw)
	}
	for _, want := range []string{`"name":"control","value":"basic"`, `"name":"pin","value":"set"`, `"code":"pin_required"`} {
		if !strings.Contains(string(res.raw), want) {
			t.Errorf("audit lacks %s: %s", want, res.raw)
		}
	}
}
