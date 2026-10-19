package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
	o := Options{Client: client, DataDir: dir, Logf: quiet}
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
		// Ranges as a Duo with firmware 1.7.6 has them.
		{"ducking in dB", map[string]interface{}{"path": l.Path("DUCKER", 0), "name": "duckerDepth", "value": -9}, ""},
		{"ducking above 0 dB", map[string]interface{}{"path": l.Path("DUCKER", 0), "name": "duckerDepth", "value": 3}, "out_of_range"},
		{"brightness 250", map[string]interface{}{"path": l.Path("GUI", 0), "name": "screenBrightness", "value": 250}, ""},
		{"brightness 300", map[string]interface{}{"path": l.Path("GUI", 0), "name": "screenBrightness", "value": 300}, "out_of_range"},
		{"auto brightness", map[string]interface{}{"path": l.Path("GUI", 0), "name": "autoBrightness", "value": 1}, ""},
		{"auto brightness 2", map[string]interface{}{"path": l.Path("GUI", 0), "name": "autoBrightness", "value": 2}, "out_of_range"},
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

// The device's buttons are used wherever their tty exists; there is no switch.
func TestPanelWhereItsTTYExists(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Panel = &hw.Panel{TTY: "/dev/null"} })
	hwDoc := r.do(t, http.MethodGet, "/api/hw", nil).body["panel"].(map[string]interface{})
	if hwDoc["enabled"] != true {
		t.Fatalf("hw panel: %v", hwDoc)
	}
	none := New(Options{Client: r.client, DataDir: t.TempDir(), Logf: quiet, Panel: &hw.Panel{TTY: "/dev/no-such-tty"}})
	if none.panelOn() {
		t.Fatal("panel on without its tty")
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
	// A copy of the desk that is not current presses no toggle: it could do
	// the opposite (unmute a live microphone).
	mirror.SetStale(true)
	if res := set(ch0, "channelOutputMute", !muted); res.code != 409 || res.body["code"] != "stale_state" || len(fp.pressed) != 2 {
		t.Fatalf("mute on a stale copy: %d %s (pressed %v)", res.code, res.raw, fp.pressed)
	}
	mirror.SetStale(false)
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

	// A SMART pad of the bank the device shows is pressed on the panel; its
	// release is part of that press. Pads beyond the physical six are not.
	var pad1, pad7 []int
	mirror.View(func(s desk.Snapshot) { pad1, pad7 = s.Layout.Path("PADBUTTON", 1), s.Layout.Path("PADBUTTON", 7) })
	press := func(path []int) resp {
		return r.post(t, "/api/press", map[string]interface{}{"epoch": epoch, "path": path, "name": "padButtonPressed", "ms": 20})
	}
	if res := press(pad1); res.code != 200 {
		t.Fatalf("pad 2: %d %s", res.code, res.raw)
	}
	if res := press(pad7); res.code != 403 || res.body["code"] != "passive" {
		t.Fatalf("pad 8: %d %s", res.code, res.raw)
	}
	time.Sleep(150 * time.Millisecond) // the release
	if got := strings.Join(fp.pressed, ","); got != "mute1,listen1,fader1,pad2" {
		t.Fatalf("pressed %v", got)
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

type fakeMeters struct {
	mu sync.Mutex
	r  hw.MeterReading
}

func (f *fakeMeters) Read(r *hw.MeterReading) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*r = f.r
	return nil
}

func (f *fakeMeters) set(fn func(r *hw.MeterReading)) {
	f.mu.Lock()
	fn(&f.r)
	f.mu.Unlock()
}

func TestMetersFromTheMixersMemory(t *testing.T) {
	fm := &fakeMeters{}
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Meters = fm })
	m := r.client.Mirror()
	sub, err := m.Subscribe() // a watching browser: read fast
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	l, _ := r.snap()
	run := l.Runs["METER"]
	get := func(p []int, name string) (v rcproto.Value) {
		m.View(func(s desk.Snapshot) { v, _ = s.Root.At(p).Get(name) })
		return
	}
	fader0, fader5 := l.Path("FADER", 0), l.Path("FADER", 5)
	virtual := get(fader5, "faderLevel")
	fm.set(func(mr *hw.MeterReading) {
		mr.Meters[0] = hw.Meter{Stereo: true, PeakL: 0.75, LevelL: 0.5, PeakR: 0.3, LevelR: 0.25}
		mr.Meters[21] = hw.Meter{Stereo: false, PeakL: 0.5, LevelL: 0.123456}
		mr.Faders[0], mr.Faders[5] = 100, 3
	})
	eventually(t, func() bool { return get(run.Path(0), "meterLevelL").Num() == 0.5 })
	if v := get(run.Path(0), "meterPeakR").Num(); v != 0.3 {
		t.Errorf("peak R %v", v)
	}
	if l, r := get(run.Path(21), "meterLevelL").Num(), get(run.Path(21), "meterLevelR").Num(); l != 0.1235 || r != l {
		t.Errorf("mono meter: L %v R %v (rounded, both sides alike)", l, r)
	}
	if v := get(fader0, "faderLevel").I; v != 100 {
		t.Errorf("physical fader %d, want the position from the meter memory", v)
	}
	if v := get(fader5, "faderLevel"); !v.Equal(virtual) {
		t.Errorf("an on-screen fader was changed: %v", v)
	}
	// Live is set just after the first reading is in the tree.
	eventually(t, func() bool { return r.do(t, "GET", "/api/status", nil).body["meters"] == true })

	if !get(run.Path(0), "meterStereo").Truth() {
		t.Error("the stereo flag comes from the meter memory too")
	}
}

func TestMetersNeedOneNodePerRecord(t *testing.T) {
	root, err := rcsim.DuoTree()
	if err != nil {
		t.Fatal(err)
	}
	l := rcproto.DiscoverLayout(root)
	var r hw.MeterReading
	if ch, ok := meterChanges(desk.Snapshot{Root: root, Layout: l}, &r); !ok || len(ch) != 5*hw.MeterRecords+l.PhysicalFaders {
		t.Fatalf("Duo tree: %d changes, %v", len(ch), ok)
	}
	run := l.Runs["METER"]
	pi := root.At(run.Parent)
	last := run.First + run.Count - 1
	pi.Children = append(pi.Children[:last], pi.Children[last+1:]...)
	if _, ok := meterChanges(desk.Snapshot{Root: root, Layout: rcproto.DiscoverLayout(root)}, &r); ok {
		t.Error("applied to a tree with another meter count")
	}
}

func TestFullControlThroughTheAPI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	sim, err := rcsim.NewDuo(rcsim.Options{OneClient: true, Logf: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Close)
	// Reading along (it cannot here, without root) as on the device.
	client := desk.NewClient(desk.Config{Addr: sim.Addr(), Mode: "tap", Logf: quiet}, desk.NewMirror())
	dir := t.TempDir()
	c := New(Options{Client: client, DataDir: dir, Logf: quiet, FullControl: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { client.Run(ctx); close(done) }()
	go c.Run(ctx)
	hs := httptest.NewServer(sdk.CSRF(c.Handler()))
	t.Cleanup(func() { cancel(); <-done; hs.Close() })
	r := &rig{sim: sim, client: client, console: c, http: hs, dir: dir}

	// Full control starts by itself; writing needs only the Control level.
	eventually(t, func() bool { st := client.Status(); return st.Mode == "relay" && st.State == desk.StateLive })
	r.control(t, "basic")
	if st := r.do(t, "GET", "/api/status", nil); st.body["passive"] != false {
		t.Fatalf("passive in full control: %v", st.body["passive"])
	}
	l, epoch := r.snap()
	ch := l.Path("CHANNEL", 6)
	cur, _ := sim.Snapshot().At(ch).Get("channelOutputMute")
	if res := r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": ch, "name": "channelOutputMute", "value": !cur.Truth()}); res.code != 200 {
		t.Fatalf("write in full control: %d %s", res.code, res.raw)
	}
	eventually(t, func() bool { v, _ := sim.Snapshot().At(ch).Get("channelOutputMute"); return v.Truth() == !cur.Truth() })
	// The old switch is gone: asking for it changes nothing.
	if res := r.post(t, "/api/settings", map[string]bool{"fullControl": false}); res.code != 200 {
		t.Fatalf("old switch: %d %s", res.code, res.raw)
	}
	time.Sleep(200 * time.Millisecond)
	if st := client.Status(); st.Mode != "relay" {
		t.Fatalf("full control ended by the old switch: %+v", st)
	}
}

// Connected (not on the device), full control is never tried.
func TestFullControlOnlyOnTheDevice(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.FullControl = true })
	time.Sleep(200 * time.Millisecond)
	if st := r.client.Status(); st.Mode != "connect" || st.FullControl {
		t.Fatalf("connected: %+v", st)
	}
}

func TestReadOnlySwitch(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	if res := r.post(t, "/api/settings", map[string]bool{"readOnly": true}); res.code != 200 {
		t.Fatalf("switch on: %d %s", res.code, res.raw)
	}
	st := r.do(t, "GET", "/api/status", nil).body
	if st["readOnly"] != true || st["readOnlyFlag"] != false || st["controlLimit"] != "read_only" || st["control"] != "off" {
		t.Fatalf("status: %v", st)
	}
	l, epoch := r.snap()
	ch := l.Path("CHANNEL", 6)
	if res := r.post(t, "/api/set", map[string]interface{}{"epoch": epoch, "path": ch, "name": "channelOutputMute", "value": true}); res.code != 403 || res.body["code"] != "read_only" {
		t.Fatalf("write while read-only: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]bool{"readOnly": false}); res.code != 200 {
		t.Fatalf("switch off: %d %s", res.code, res.raw)
	}
	if st := r.do(t, "GET", "/api/status", nil).body; st["readOnly"] != false || st["control"] != "basic" {
		t.Fatalf("after switching off: %v", st)
	}
}

func TestPinResetAtTheDevice(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	if res := r.post(t, "/api/pin/reset", nil); res.code != 409 || res.body["code"] != "no_pin" {
		t.Fatalf("reset without a PIN: %d %s", res.code, res.raw)
	}
	r.control(t, "expert")
	if res := r.post(t, "/api/pin", map[string]string{"new": "4711"}); res.code != 200 {
		t.Fatalf("set PIN: %d %s", res.code, res.raw)
	}
	l, _ := r.snap()
	btn := l.Path("SOLOMUTEBUTTON", 0)
	press := func() {
		for _, v := range []bool{true, false} {
			if err := r.sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: btn, Name: "soloPressed", Value: rcproto.Bool(v)}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	press() // before the request: does not count
	if res := r.post(t, "/api/pin/reset", nil); res.code != 200 {
		t.Fatalf("start reset: %d %s", res.code, res.raw)
	}
	press()
	press()
	press()
	time.Sleep(100 * time.Millisecond)
	st := r.do(t, "GET", "/api/status", nil).body
	if st["pinRequired"] != true || st["pinReset"].(map[string]interface{})["presses"] != float64(3) {
		t.Fatalf("after three presses: %v", st["pinReset"])
	}
	press()
	eventually(t, func() bool { return r.do(t, "GET", "/api/status", nil).body["pinRequired"] == false })
	if st := r.do(t, "GET", "/api/status", nil).body; st["control"] != "off" || st["pinReset"].(map[string]interface{})["done"] == nil {
		t.Fatalf("after the reset: %v", st)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "pin")); !os.IsNotExist(err) {
		t.Fatalf("PIN file still there: %v", err)
	}

	// Presses spread out (normal use) do not reset it.
	if res := r.post(t, "/api/pin", map[string]string{"new": "4711"}); res.code != 200 {
		t.Fatalf("set PIN again: %d %s", res.code, res.raw)
	}
	old := pinResetBurst.Load()
	pinResetBurst.Store(int64(50 * time.Millisecond))
	defer pinResetBurst.Store(old)
	if res := r.post(t, "/api/pin/reset", nil); res.code != 200 {
		t.Fatalf("start reset: %d %s", res.code, res.raw)
	}
	for i := 0; i < 5; i++ {
		press()
		time.Sleep(60 * time.Millisecond)
	}
	if st := r.do(t, "GET", "/api/status", nil).body; st["pinRequired"] != true {
		t.Fatal("spread-out presses reset the PIN")
	}
}

func TestPresenceOnlyForTheHost(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	if res := r.post(t, "/api/presence", nil, "X-Forwarded-For", "192.168.1.30"); res.code != 403 || res.body["code"] != "host_only" {
		t.Fatalf("through the proxy: %d %s", res.code, res.raw)
	}
	l, _ := r.snap()
	btn := l.Path("SOLOMUTEBUTTON", 0)
	done := make(chan resp, 1)
	go func() { done <- r.post(t, "/api/presence", nil) }()
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 4; i++ {
		for _, v := range []bool{true, false} {
			if err := r.sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: btn, Name: "soloPressed", Value: rcproto.Bool(v)}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	select {
	case res := <-done:
		if res.code != 200 {
			t.Fatalf("presence: %d %s", res.code, res.raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("presses not reported")
	}
}

type fakeBus struct {
	mu     sync.Mutex
	events []string
	data   []map[string]interface{}
}

func (f *fakeBus) publish(ctx context.Context, typ string, data interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, typ)
	m, _ := data.(map[string]interface{})
	f.data = append(f.data, m)
	return nil
}

func (f *fakeBus) seen(typ string) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, e := range f.events {
		if e == typ {
			return f.data[i]
		}
	}
	return nil
}

func TestDeskEvents(t *testing.T) {
	fb := &fakeBus{}
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Publish = fb.publish })
	eventually(t, func() bool { return fb.seen("desk.connection.live") != nil })
	l, _ := r.snap()
	set := func(path []int, name string, v rcproto.Value) {
		t.Helper()
		if err := r.sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: name, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	rec := l.Path("RECORDER", 0)
	set(rec, "recordState", rcproto.Int(2))
	eventually(t, func() bool { return fb.seen("desk.recording.started") != nil })
	set(rec, "recordState", rcproto.Int(1))
	set(rec, "recordState", rcproto.Int(2))
	set(rec, "requestDropMarker", rcproto.Int(1234))
	set(rec, "recordState", rcproto.Int(0))
	eventually(t, func() bool { return fb.seen("desk.recording.stopped") != nil })
	for _, typ := range []string{"desk.recording.paused", "desk.recording.resumed", "desk.recording.marker"} {
		if fb.seen(typ) == nil {
			t.Errorf("no %s", typ)
		}
	}
	ch := l.Path("CHANNEL", 0)
	cur, _ := r.sim.Snapshot().At(ch).Get("channelOutputMute")
	set(ch, "channelOutputMute", rcproto.Bool(!cur.Truth()))
	eventually(t, func() bool { return fb.seen("desk.channel.mute") != nil })
	if d := fb.seen("desk.channel.mute"); d["strip"] != 0 || d["label"] != "Fader 1" || d["muted"] != !cur.Truth() {
		t.Errorf("mute event %v", d)
	}
	set(l.Path("PADBUTTON", 2), "padButtonPressed", rcproto.Bool(true))
	eventually(t, func() bool { return fb.seen("desk.pad.pressed") != nil })
	if d := fb.seen("desk.pad.pressed"); d["pad"] != 2 {
		t.Errorf("pad event %v", d)
	}
	// Meters and other properties make no events.
	fb.mu.Lock()
	n := len(fb.events)
	fb.mu.Unlock()
	set(l.Path("FADER", 5), "faderLevel", rcproto.Int(33))
	time.Sleep(150 * time.Millisecond)
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.events) != n {
		t.Errorf("unexpected events %v", fb.events[n:])
	}
}

func TestPluginsNeedDeskWrite(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Hosted = true })
	l, epoch := r.snap()
	ch := l.Path("CHANNEL", 6)
	body := map[string]interface{}{"epoch": epoch, "path": ch, "name": "channelOutputMute", "value": true}
	asPlugin := func(perms string) []string {
		return []string{"X-Forwarded-For", "127.0.0.1", "X-Rcplug-Plugin", "scenes", "X-Rcplug-Permissions", perms}
	}
	// The browser's level is "off": a plugin with desk:write still writes.
	if res := r.post(t, "/api/set", body, asPlugin("audio")...); res.code != 403 || res.body["code"] != "permission_desk_write" {
		t.Fatalf("without desk:write: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/set", body, asPlugin("audio,desk:write")...); res.code != 200 {
		t.Fatalf("with desk:write: %d %s", res.code, res.raw)
	}
	if a := r.do(t, "GET", "/api/audit", nil).raw; !strings.Contains(string(a), "plugin scenes") {
		t.Errorf("audit does not name the plugin: %s", a)
	}
	// Straight to the console's port, past rcplugd: an unknown plugin.
	if res := r.post(t, "/api/set", body); res.code != 403 || res.body["code"] != "permission_desk_write" {
		t.Fatalf("past rcplugd: %d %s", res.code, res.raw)
	}
	// A browser on the device itself is a person.
	if res := r.post(t, "/api/set", body, "Sec-Fetch-Mode", "cors"); res.code != 403 || res.body["code"] == "permission_desk_write" {
		t.Fatalf("a browser on the device: %d %s", res.code, res.raw)
	}
	// People from the network: the level applies (off here).
	if res := r.post(t, "/api/set", body, "X-Forwarded-For", "192.168.1.30"); res.code != 403 || res.body["code"] == "permission_desk_write" {
		t.Fatalf("a person at level off: %d %s", res.code, res.raw)
	}
	// Plugins never change the console's settings.
	for _, p := range []string{"/api/control", "/api/settings", "/api/pin", "/api/pin/reset"} {
		if res := r.post(t, p, map[string]string{"level": "expert"}, asPlugin("desk:write")...); res.code != 403 || res.body["code"] != "plugin_not_allowed" {
			t.Errorf("%s by a plugin: %d %s", p, res.code, res.raw)
		}
	}
	// With a PIN set, the plugin needs none; write protection stops it.
	if res := r.post(t, "/api/pin", map[string]string{"new": "4711"}, "X-Forwarded-For", "192.168.1.30"); res.code != 200 {
		t.Fatalf("set PIN: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/set", body, asPlugin("desk:write")...); res.code != 200 {
		t.Fatalf("plugin with a PIN set: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/settings", map[string]bool{"readOnly": true}, "X-Forwarded-For", "192.168.1.30", "X-Rcplug-Pin", "4711"); res.code != 200 {
		t.Fatalf("write protection: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/set", body, asPlugin("desk:write")...); res.code != 403 || res.body["code"] != "read_only" {
		t.Fatalf("plugin under write protection: %d %s", res.code, res.raw)
	}
}

func TestChangeLog(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(*Options) {})
	l, _ := r.snap()
	inject := func(path []int, name string, v rcproto.Value) {
		if err := r.sim.Inject(&rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: name, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	in := l.Path("INPUTSOURCE", 1)
	old, _ := r.sim.Snapshot().At(in).Get("inputMicrophoneType")
	inject(in, "inputMicrophoneType", rcproto.Int(old.I+3))
	inject(l.Path("FADER", 0), "faderLevel", rcproto.Int(77))    // continuous: left out
	inject(l.Path("NETWORK", 0), "wifiPSK", rcproto.String("x")) // a secret
	var got []ChangeEntry
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		res := r.do(t, http.MethodGet, "/api/changes", nil)
		got = nil
		json.Unmarshal(res.raw, &struct{ Changes *[]ChangeEntry }{&got})
		if len(got) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("changes: %s", res.raw)
		}
	}
	e := got[0]
	if e.Node != "INPUTSOURCE" || e.Index != 1 || e.Name != "inputMicrophoneType" || string(e.Old) != strconv.FormatInt(old.I, 10) || string(e.New) != strconv.FormatInt(old.I+3, 10) {
		t.Fatalf("entry %+v", e)
	}
	if s := got[1]; s.Name != "wifiPSK" || strings.Contains(string(s.New), `"x"`) {
		t.Fatalf("secret %+v", s)
	}
	for _, e := range got {
		if e.Name == "faderLevel" {
			t.Fatal("a fader position was logged")
		}
	}
	if !strings.Contains(string(r.console.changesForDiagnostics()), `"inputMicrophoneType"`) {
		t.Fatal("not in the diagnostics")
	}
	if res := r.post(t, "/api/changes/clear", map[string]string{}); res.code != 200 {
		t.Fatalf("clear: %d", res.code)
	}
	if res := r.do(t, http.MethodGet, "/api/changes", nil); strings.Contains(string(res.raw), "inputMicrophoneType") {
		t.Fatal("not cleared")
	}
}

// A held pad: the browser presses with a long hold and releases itself; the
// hold's own late release does not let go of a newer press.
func TestHeldPressIsReleasedByTheNewestOnly(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	l, epoch := r.snap()
	pad := l.Path("PADBUTTON", 0)
	value := func() bool { v, _ := r.sim.Snapshot().At(pad).Get("padButtonPressed"); return v.Truth() }
	press := func(ms int) {
		if res := r.post(t, "/api/press", map[string]interface{}{"epoch": epoch, "path": pad, "name": "padButtonPressed", "ms": ms}); res.code != 200 {
			t.Fatalf("press: %d %s", res.code, res.raw)
		}
	}
	release := func() {
		if res := r.post(t, "/api/release", map[string]interface{}{"epoch": epoch, "path": pad, "name": "padButtonPressed"}); res.code != 200 || !strings.Contains(string(res.raw), `"released":true`) {
			t.Fatalf("release: %d %s", res.code, res.raw)
		}
	}
	press(300) // held …
	eventually(t, value)
	release() // … and let go by the browser
	eventually(t, func() bool { return !value() })
	press(5000) // held again before the first hold's own release is due
	time.Sleep(500 * time.Millisecond)
	if !value() {
		t.Fatal("the first hold's late release let go of the second press")
	}
	release()
	eventually(t, func() bool { return !value() })
}

// A hold is let go of whatever happens meanwhile: control turned off, or a
// structural change that moves the button (the release follows it and
// leaves the node now at its old place alone).
func TestHeldPressIsAlwaysReleased(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	r.control(t, "basic")
	l, epoch := r.snap()
	pad := l.Path("PADBUTTON", 0)
	pressed := func(p []int) bool { v, _ := r.sim.Snapshot().At(p).Get("padButtonPressed"); return v.Truth() }
	req := map[string]interface{}{"epoch": epoch, "path": pad, "name": "padButtonPressed"}
	if res := r.post(t, "/api/release", req); res.code != 200 || !strings.Contains(string(res.raw), `"released":false`) {
		t.Fatalf("release of nothing: %d %s", res.code, res.raw)
	}
	hold := func() {
		if res := r.post(t, "/api/press", map[string]interface{}{"epoch": epoch, "path": pad, "name": "padButtonPressed", "ms": 20000}); res.code != 200 {
			t.Fatalf("press: %d %s", res.code, res.raw)
		}
		eventually(t, func() bool { return pressed(pad) })
	}

	hold()
	r.control(t, "off")
	if res := r.post(t, "/api/release", req); res.code != 200 || !strings.Contains(string(res.raw), `"released":true`) {
		t.Fatalf("release with control off: %d %s", res.code, res.raw)
	}
	eventually(t, func() bool { return !pressed(pad) })

	r.control(t, "basic")
	hold()
	run := l.Runs["PADBUTTON"]
	if err := r.sim.Inject(&rcproto.Change{Type: rcproto.ChildAdded, Path: run.Parent, Index: run.First,
		Tree: &rcproto.Node{Type: "SPACER", Props: []rcproto.Property{{Name: "padButtonPressed", Value: rcproto.Bool(true)}}}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { _, e := r.snap(); return e != epoch })
	moved := run.Path(1)
	if res := r.post(t, "/api/release", req); res.code != 200 || !strings.Contains(string(res.raw), `"released":true`) {
		t.Fatalf("release after the tree changed: %d %s", res.code, res.raw)
	}
	eventually(t, func() bool { return !pressed(moved) })
	if !pressed(pad) {
		t.Fatal("the release went to the node now at the old place")
	}
}
