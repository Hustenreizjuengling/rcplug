package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Snapshots of the desk's settings, for backing them up and restoring them
// (the backup plugin uses these from the browser, so a person's control
// level and PIN apply).
//
// A snapshot lists every setting a person may change at the expert level,
// addressed by node family and index ("CHANNEL" 2) rather than by raw tree
// path, so it still fits after a firmware update that moves nodes. It leaves
// out what is not a setting: button presses and requests, what the device's
// screen shows, the emergency mute, the player, readings, the physical
// faders' positions and the routing levels they set (the hardware decides
// them), file paths and secrets, and
// what the console never writes (network, accounts, updates, shows; see
// policy.go). It leaves the listening volumes alone, so a restore never
// makes the headphones loud.
// Restoring writes only what differs, through the same checks as every other
// write; a routing cell's link is restored with the sequence the device
// expects (as api/link does).

// SnapshotFormat is the version of the snapshot document.
const SnapshotFormat = 1

// DeskSnapshot is a snapshot of the desk's settings.
type DeskSnapshot struct {
	Format   int             `json:"format"`
	Created  string          `json:"created"`
	Model    string          `json:"model"`
	Firmware string          `json:"firmware"`
	Settings []SnapshotEntry `json:"settings"`
}

// SnapshotEntry is one setting. For a routing cell Node is "MIX" and Index
// is source × outputs + output.
type SnapshotEntry struct {
	Node  string          `json:"node"`
	Index int             `json:"index"`
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// transientProps are writable properties that are not settings.
var transientProps = set("requestRecordState", "requestDropMarker", "padButtonPressed", "padButtonPreview",
	"mutePressed", "soloPressed", "recButtonPressed", "encoderPressed", "encoderSignal", "encoderColour",
	"screenTouched", "selectedBank", "systemChannelSelected", "systemMixSelected", "assignableMeterSource",
	"padActiveEdit", "eqParamModeLow", "eqParamModeMid", "eqParamModeHigh", "mixLinkRequest", "mixUnlinkRequest",
	"mixRcSyncLevelRequest", "padProgressRequestSignal", "emergencyMuteActive", "broadcastMeters", "metering")

// transientTypes are node types that hold no settings worth restoring. POT
// is the headphones' volume: like the monitors' (keptProps), a restore never
// turns it up all of a sudden.
var transientTypes = set("PLAYER", "PADRECORDER", "RECBUTTON", "SOLOMUTEBUTTON", "PADBUTTON", "ENCODER", "EMERGENCYMUTE", "POT")

// keptProps are settings a restore leaves as they are: the listening
// volumes, and numbers that say which input, pad or effect slot a node is
// (the device's, not the user's).
var keptProps = set("headphone1Level", "outputMonLevel", "inputId", "padIdx", "effectsIdx", "padIsInternal")

// snapshotWorthy reports whether a property of a node belongs in a snapshot.
func snapshotWorthy(nodeType, name string, v rcproto.Value) bool {
	if transientTypes[nodeType] || transientProps[name] || keptProps[name] {
		return false
	}
	switch v.Kind {
	case rcproto.KindBoolTrue, rcproto.KindBoolFalse, rcproto.KindInt, rcproto.KindInt64, rcproto.KindDouble, rcproto.KindString:
	default:
		return false // triggers and blobs
	}
	if nodeType == "MIX" && name == "mixLink" {
		return true // restored with the link sequence
	}
	return Writable(LevelExpert, nodeType, name)
}

// physicalSources are the sources on the physical faders now. Their routing
// levels follow the faders (protocol.md), so snapshots leave those out: a
// restore would move a level away from where its fader stands.
func physicalSources(s desk.Snapshot) map[int]bool {
	out := map[int]bool{}
	for i := 0; i < s.Layout.PhysicalFaders; i++ {
		if ch := s.Layout.Node(s.Root, "CHANNEL", i); ch != nil {
			if v, ok := ch.Get("channelInputSource"); ok && v.IsNumber() {
				out[int(v.Num())] = true
			}
		}
	}
	return out
}

// faderLevel reports whether a setting is a routing level that a physical
// fader sets.
func faderLevel(l *rcproto.Layout, phys map[int]bool, e SnapshotEntry) bool {
	return e.Node == "MIX" && e.Name == "mixLevelWithAnchor" && l.MixPerSource > 0 && phys[e.Index/l.MixPerSource]
}

// takeSnapshot reads the desk's settings from the mirror.
func takeSnapshot(s desk.Snapshot) DeskSnapshot {
	snap := DeskSnapshot{Format: SnapshotFormat, Created: time.Now().Format(time.RFC3339), Settings: []SnapshotEntry{}}
	if s.Root == nil || s.Layout == nil {
		return snap
	}
	l := s.Layout
	snap.Model, snap.Firmware = string(l.Model), l.Firmware
	seen, phys := map[string]bool{}, physicalSources(s)
	add := func(family string, i int, path []int) {
		n := s.Root.At(path)
		if n == nil {
			return
		}
		key := pathKey(path)
		if seen[key] {
			return
		}
		seen[key] = true
		for _, p := range n.Props {
			if !snapshotWorthy(n.Type, p.Name, p.Value) || Secret(n.Type, p.Name) {
				continue
			}
			if n.Type == "FADER" && p.Name == "faderLevel" && family == "FADER" && i < l.PhysicalFaders {
				continue // the hardware sets a physical fader
			}
			e := SnapshotEntry{Node: family, Index: i, Name: p.Name}
			if faderLevel(l, phys, e) {
				continue
			}
			var b bytes.Buffer
			desk.WriteValue(&b, p.Value, false, false)
			e.Value = json.RawMessage(b.Bytes())
			snap.Settings = append(snap.Settings, e)
		}
	}
	// Routing cells first: they are spread over the tree, the layout knows them.
	for src := 0; src < l.SourceCount; src++ {
		for out := 0; out < l.MixPerSource; out++ {
			if p := l.MixPath(src, out); p != nil {
				add("MIX", src*l.MixPerSource+out, p)
			}
		}
	}
	families := make([]string, 0, len(l.Runs))
	for f := range l.Runs {
		families = append(families, f)
	}
	sort.Strings(families) // "CHANNEL" before "PARENT/CHILD": the short name wins a node seen twice
	for _, f := range families {
		if f == "MIX" {
			continue
		}
		r := l.Runs[f]
		for i := 0; i < r.Count; i++ {
			add(f, i, r.Path(i))
		}
	}
	return snap
}

func pathKey(p []int) string {
	b, _ := json.Marshal(p)
	return string(b)
}

// resolve finds a snapshot entry's node in the current tree.
func resolve(l *rcproto.Layout, e SnapshotEntry) []int {
	if e.Node == "MIX" {
		if l.MixPerSource <= 0 {
			return nil
		}
		return l.MixPath(e.Index/l.MixPerSource, e.Index%l.MixPerSource)
	}
	r, ok := l.Runs[e.Node]
	if !ok || e.Index < 0 || e.Index >= r.Count {
		return nil
	}
	return r.Path(e.Index)
}

// SnapshotDiff is a setting that differs from the snapshot.
type SnapshotDiff struct {
	SnapshotEntry
	Current json.RawMessage `json:"current"`
	Missing bool            `json:"missing,omitempty"` // the desk has no such setting now
	Expert  bool            `json:"expert,omitempty"`  // only the expert level writes it
	// Label names the node for people: "Mic 1 → Headphones 2" for a
	// routing cell, "Fader 1 (Mic 1)" for a channel.
	Label string `json:"label,omitempty"`
}

// diffLabel names a setting's node for the diff.
func diffLabel(s desk.Snapshot, e SnapshotEntry) string {
	l := s.Layout
	switch e.Node {
	case "MIX":
		if l.MixPerSource > 0 {
			src, out := e.Index/l.MixPerSource, e.Index%l.MixPerSource
			if out < len(rcproto.Outputs) {
				return rcproto.SourceName(int64(src)).Label + " → " + rcproto.Outputs[out].Label
			}
		}
	case "CHANNEL", "FADER":
		if f := l.Faders(); e.Index < len(f) {
			label := f[e.Index].Label
			if ch := l.Node(s.Root, "CHANNEL", e.Index); ch != nil {
				if v, ok := ch.Get("channelInputSource"); ok && v.IsNumber() && v.Num() >= 0 {
					label += " (" + rcproto.SourceName(int64(v.Num())).Label + ")"
				}
			}
			return label
		}
	}
	return ""
}

// diffSnapshot compares a snapshot with the desk.
func diffSnapshot(s desk.Snapshot, snap DeskSnapshot) []SnapshotDiff {
	out := []SnapshotDiff{}
	if s.Root == nil || s.Layout == nil {
		return out
	}
	phys := physicalSources(s)
	for _, e := range snap.Settings {
		if faderLevel(s.Layout, phys, e) {
			continue // its fader decides now
		}
		path := resolve(s.Layout, e)
		var n *rcproto.Node
		if path != nil {
			n = s.Root.At(path)
		}
		if n == nil {
			out = append(out, SnapshotDiff{SnapshotEntry: e, Missing: true})
			continue
		}
		cur, ok := n.Get(e.Name)
		if !ok {
			out = append(out, SnapshotDiff{SnapshotEntry: e, Missing: true})
			continue
		}
		var b bytes.Buffer
		desk.WriteValue(&b, cur, false, false)
		if !sameJSON(b.Bytes(), e.Value) {
			name := e.Name
			if n.Type == "MIX" && name == "mixLink" {
				name = "mixLinkRequest"
			}
			out = append(out, SnapshotDiff{SnapshotEntry: e, Current: json.RawMessage(b.Bytes()), Expert: !Writable(LevelBasic, n.Type, name),
				Label: diffLabel(s, e)})
		}
	}
	return out
}

// sameJSON compares two JSON values; numbers within a float32's precision
// count as equal (the desk stores many as float32).
func sameJSON(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	fx, okx := x.(float64)
	fy, oky := y.(float64)
	if okx && oky {
		return math.Abs(fx-fy) <= 1e-6*math.Max(1, math.Max(math.Abs(fx), math.Abs(fy)))
	}
	return x == y
}

func (c *Console) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	var snap DeskSnapshot
	c.mirror.View(func(s desk.Snapshot) { snap = takeSnapshot(s) })
	if len(snap.Settings) == 0 {
		sdk.WriteError(w, desk.ErrNotSynced)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, snap)
}

func readSnapshot(w http.ResponseWriter, r *http.Request) (DeskSnapshot, bool) {
	var snap DeskSnapshot
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&snap); err != nil {
		sdk.WriteError(w, errBadRequest("invalid snapshot: "+err.Error()))
		return snap, false
	}
	if snap.Format != SnapshotFormat {
		sdk.WriteError(w, errBadRequest("unknown snapshot format"))
		return snap, false
	}
	return snap, true
}

// handleSnapshotDiff: POST api/snapshot/diff with a snapshot answers the
// settings that differ, and what restoring needs: the level the requester
// writes at, whether the console has full control (not passive) and the PIN.
func (c *Console) handleSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	snap, ok := readSnapshot(w, r)
	if !ok {
		return
	}
	level, _ := c.levelFor(c.who(r))
	var diffs []SnapshotDiff
	var model string
	c.mirror.View(func(s desk.Snapshot) {
		diffs = diffSnapshot(s, snap)
		if s.Layout != nil {
			model = string(s.Layout.Model)
		}
	})
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"differences": diffs, "sameModel": model == snap.Model, "model": model,
		"level": level.String(), "passive": c.client.Passive(), "pinRequired": c.pin.set()})
}

// Restoring: a setting can change others (a microphone type applies its
// preset to the gain and the voice effects; linking a routing cell unmutes
// it), so the restore looks at the desk again after each pass and writes
// what still differs. Within a pass links come first, then the types, then
// the rest; at the basic level a value with a step limit (the gains) moves
// there in allowed steps.
const restorePasses = 4

var (
	restorePause = 400 * time.Millisecond // for the desk's own follow-up changes
	restoreGap   = 4 * time.Millisecond   // between two writes, to go easy on the mixer
)

func restoreOrder(name string) int {
	switch {
	case name == "mixLink":
		return 0
	case strings.HasSuffix(name, "Type"):
		return 1
	}
	return 2
}

// SnapshotRefusal is a setting the restore could not write.
type SnapshotRefusal struct {
	SnapshotEntry
	Code  string `json:"code"`
	Error string `json:"error"`
}

// SnapshotResult says what a restore did.
type SnapshotResult struct {
	Written   int               `json:"written"`
	Refused   []SnapshotRefusal `json:"refused"`
	Remaining int               `json:"remaining"` // still different after the last pass (refused ones not counted)
	Missing   int               `json:"missing"`   // settings this desk does not have
}

func (e SnapshotEntry) key() string { return e.Node + "#" + strconv.Itoa(e.Index) + "." + e.Name }

// restore writes a snapshot's settings that differ from the desk.
func (c *Console) restore(ctx context.Context, by who, snap DeskSnapshot) (SnapshotResult, error) {
	res := SnapshotResult{Refused: []SnapshotRefusal{}}
	level, _ := c.levelFor(by)
	refused, written := map[string]bool{}, map[string]bool{}
	pending := func() (diffs []SnapshotDiff, epoch uint64, layout *rcproto.Layout) {
		c.mirror.View(func(s desk.Snapshot) { diffs, epoch, layout = diffSnapshot(s, snap), s.Epoch, s.Layout })
		todo := diffs[:0]
		res.Missing = 0
		for _, d := range diffs {
			switch {
			case d.Missing:
				res.Missing++
			case !refused[d.key()]:
				todo = append(todo, d)
			}
		}
		return todo, epoch, layout
	}
	refuse := func(e SnapshotEntry, err error) {
		code := "refused"
		var ae *sdk.APIError
		if errors.As(err, &ae) {
			code = ae.Code
		}
		refused[e.key()] = true
		res.Refused = append(res.Refused, SnapshotRefusal{e, code, err.Error()})
	}
	for pass := 0; pass < restorePasses; pass++ {
		if pass > 0 {
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(restorePause):
			}
		}
		diffs, epoch, layout := pending()
		if layout == nil {
			return res, desk.ErrNotSynced
		}
		if len(diffs) == 0 {
			break
		}
		sort.SliceStable(diffs, func(i, j int) bool { return restoreOrder(diffs[i].Name) < restoreOrder(diffs[j].Name) })
		wrote := 0
	writes:
		for _, d := range diffs {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			path := resolve(layout, d.SnapshotEntry)
			var v interface{}
			if err := json.Unmarshal(d.Value, &v); err != nil {
				refuse(d.SnapshotEntry, sdk.NewError(http.StatusBadRequest, "invalid_value", err.Error()))
				continue
			}
			err := c.restoreOne(by, level, epoch, path, d, v)
			var ae *sdk.APIError
			switch {
			case errors.As(err, &ae) && ae.Code == "stale_epoch":
				break writes // the tree was rebuilt: look again
			case err != nil:
				refuse(d.SnapshotEntry, err)
				continue
			}
			written[d.key()] = true
			wrote++
			time.Sleep(restoreGap)
		}
		if wrote == 0 {
			break
		}
	}
	diffs, _, _ := pending()
	res.Written, res.Remaining = len(written), len(diffs)
	return res, nil
}

// restoreOne writes one setting: a link with the sequence the device
// expects, a value with a step limit in steps.
func (c *Console) restoreOne(by who, level Level, epoch uint64, path []int, d SnapshotDiff, v interface{}) error {
	if d.Node == "MIX" && d.Name == "mixLink" {
		if on, _ := v.(bool); !on {
			return c.pulse(by, epoch, path, "mixUnlinkRequest")
		}
		if _, err := c.write(by, epoch, path, "mixDisabled", false); err != nil {
			return err
		}
		if _, err := c.write(by, epoch, path, "mixMute", false); err != nil {
			return err
		}
		return c.pulse(by, epoch, path, "mixLinkRequest")
	}
	target, isNum := v.(float64)
	var cur float64
	if json.Unmarshal(d.Current, &cur) != nil {
		isNum = false
	}
	nodeType := ""
	c.mirror.View(func(s desk.Snapshot) {
		if n := s.Root.At(path); n != nil {
			nodeType = n.Type
		}
	})
	step := stepLimit(level, nodeType, d.Name)
	if !isNum || step <= 0 {
		_, err := c.write(by, epoch, path, d.Name, v)
		return err
	}
	for i := 0; cur != target && i < 64; i++ {
		next := target
		if next > cur+step {
			next = cur + step
		} else if next < cur-step {
			next = cur - step
		}
		if _, err := c.write(by, epoch, path, d.Name, next); err != nil {
			return err
		}
		cur = next
	}
	return nil
}

// handleSnapshotApply: POST api/snapshot/apply with a snapshot writes the
// settings that differ, at the requester's level (SnapshotResult).
func (c *Console) handleSnapshotApply(w http.ResponseWriter, r *http.Request) {
	snap, ok := readSnapshot(w, r)
	if !ok {
		return
	}
	if c.client.Passive() {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "needs_full_control", "restoring the desk's settings needs full control (the console only reads along now)"))
		return
	}
	var model string
	c.mirror.View(func(s desk.Snapshot) {
		if s.Layout != nil {
			model = string(s.Layout.Model)
		}
	})
	if snap.Model != model {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "other_model", fmt.Sprintf("the snapshot is of a %s, this desk is a %s", snap.Model, model)))
		return
	}
	if !c.restoring.TryLock() {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "busy", "a restore is already running"))
		return
	}
	defer c.restoring.Unlock()
	by := c.who(r)
	res, err := c.restore(r.Context(), by, snap)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	c.opts.Logf("console: snapshot restored by %s: %d written, %d refused, %d still different, %d missing",
		by.label(), res.Written, len(res.Refused), res.Remaining, res.Missing)
	sdk.WriteJSON(w, http.StatusOK, res)
}
