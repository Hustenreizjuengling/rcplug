package console

import (
	"net/http"
	"testing"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

// The screen plugin adds a preset node and removes it again; others cannot,
// and no other kinds of nodes are added.
func TestNodesForScreenPlugin(t *testing.T) {
	r := newRig(t, rcsim.Options{}, func(o *Options) { o.Hosted = true })
	asPlugin := func(perms string) []string {
		return []string{"X-Forwarded-For", "127.0.0.1", "X-Rcplug-Plugin", "screen", "X-Rcplug-Permissions", perms}
	}
	presets := r.pathOf("FXPRESETS")
	if presets == nil {
		t.Skip("the simulated desk has no FXPRESETS node")
	}
	count := func() (int, uint64) {
		var n int
		var epoch uint64
		r.client.Mirror().View(func(s desk.Snapshot) {
			n, epoch = len(s.Root.At(presets).Children), s.Epoch
		})
		return n, epoch
	}
	before, epoch := count()
	add := map[string]interface{}{"epoch": epoch, "path": presets, "type": "FXPRESET",
		"props": map[string]interface{}{"fxPresetIdx": before, "fxPresetContents": "Test|0.5|0.5|0.5"}}

	if res := r.post(t, "/api/node/add", add); res.code != http.StatusForbidden || res.body["code"] != "screen_required" {
		t.Fatalf("a browser: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/node/add", add, asPlugin("desk:write")...); res.code != http.StatusForbidden || res.body["code"] != "screen_required" {
		t.Fatalf("a plugin without the screen: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/node/add", add, asPlugin("screen")...); res.code != http.StatusForbidden || res.body["code"] != "permission_desk_write" {
		t.Fatalf("the screen without desk:write: %d %s", res.code, res.raw)
	}
	res := r.post(t, "/api/node/add", add, asPlugin("desk:write,screen")...)
	if res.code != http.StatusOK {
		t.Fatalf("the screen plugin adds: %d %s", res.code, res.raw)
	}
	after, epoch2 := count()
	if after != before+1 || epoch2 == epoch {
		t.Fatalf("after the add: %d children (was %d), epoch %d → %d", after, before, epoch, epoch2)
	}
	var added *rcproto.Node
	r.client.Mirror().View(func(s desk.Snapshot) { added = s.Root.At(presets).Children[after-1] })
	if v, ok := added.Get("fxPresetContents"); !ok || v.S != "Test|0.5|0.5|0.5" || added.Type != "FXPRESET" {
		t.Fatalf("added node %+v", added)
	}
	if v, ok := added.Get("fxPresetIdx"); !ok || v.Kind != rcproto.KindInt || v.I != int64(before) {
		t.Fatalf("fxPresetIdx should follow its siblings' int type: %+v", v)
	}
	// The simulated desk took it too (the frame travels over TCP).
	got := false
	for i := 0; i < 100 && !got; i++ {
		for _, w := range r.sim.Writes() {
			if w.Type == rcproto.ChildAdded {
				got = true
			}
		}
		if !got {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !got {
		t.Fatalf("the desk did not get the child: %v", r.sim.Writes())
	}

	// Other kinds are refused, and so is a stale epoch.
	bad := map[string]interface{}{"epoch": epoch2, "path": presets, "type": "SHOW", "props": map[string]interface{}{}}
	if res := r.post(t, "/api/node/add", bad, asPlugin("desk:write,screen")...); res.code != http.StatusForbidden || res.body["code"] != "node_kind" {
		t.Fatalf("a SHOW under FXPRESETS: %d %s", res.code, res.raw)
	}
	add["epoch"] = epoch
	if res := r.post(t, "/api/node/add", add, asPlugin("desk:write,screen")...); res.code == http.StatusOK {
		t.Fatalf("a stale epoch must be refused: %s", res.raw)
	}

	// Remove it again.
	nodePath := append(append([]int(nil), presets...), after-1)
	rm := map[string]interface{}{"epoch": epoch2, "path": nodePath}
	if res := r.post(t, "/api/node/remove", rm); res.code != http.StatusForbidden {
		t.Fatalf("a browser removes: %d %s", res.code, res.raw)
	}
	if res := r.post(t, "/api/node/remove", rm, asPlugin("desk:write,screen")...); res.code != http.StatusOK {
		t.Fatalf("the screen plugin removes: %d %s", res.code, res.raw)
	}
	if n, _ := count(); n != before {
		t.Fatalf("after the remove: %d children, want %d", n, before)
	}
	// A node that is not a managed kind is never removed.
	sys := r.pathOf("SYSTEM")
	_, epoch3 := count()
	if res := r.post(t, "/api/node/remove", map[string]interface{}{"epoch": epoch3, "path": sys}, asPlugin("desk:write,screen")...); res.code != http.StatusForbidden {
		t.Fatalf("removing SYSTEM: %d %s", res.code, res.raw)
	}
}
