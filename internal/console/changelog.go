package console

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// The change log: the last property changes of the desk with the value
// before and after, for working out what a setting's numbers mean. Change
// something on the device's screen and the Log page shows, say,
// "INPUTSOURCE #0 inputMicrophoneType 8 → 4"; the diagnostics bundle carries
// the same (strings left out). Continuous readings (meters, fader positions,
// progress, the recording time) are left out, so a few hundred entries cover
// a while of operating the device.

const changeLogSize = 500

// noisyProps change continuously while the desk runs.
var noisyProps = set("faderLevel", "meterLevelL", "meterLevelR", "meterPeakL", "meterPeakR", "meterStereo",
	"mixLevelWithAnchor", "padProgress", "recordTimeMs", "recordBytesPerSecond", "playerProgress",
	"playerCurrentPositionTime", "storageVolumeFree", "storageVolumeState", "encoderSignal", "screenTouched",
	"updateDownloadProgress", "updateInstalledProgress", "showControlProgress", "txRssi", "txSignalQuality",
	"txBatteryLevel", "padRecordSeconds", "sipRemainingCalltime", "sipSlotCallJitter", "sipSlotCallPacketLoss",
	"sipSlotCallQuality", "sipSlotCallBitrate", "systemDateTime")

// ChangeEntry is one logged change.
type ChangeEntry struct {
	Time  string          `json:"time"`
	Node  string          `json:"node"`
	Index int             `json:"index"` // in its family; -1 for a single node
	Path  []int           `json:"path"`
	Name  string          `json:"name"`
	Old   json.RawMessage `json:"old"`
	New   json.RawMessage `json:"new"`
	// Text: the values are strings (left out of the diagnostics bundle).
	Text bool `json:"text,omitempty"`
}

type rawChange struct {
	at         time.Time
	path       []int
	name, node string
	index      int
	old, new   rcproto.Value
}

type changeLog struct {
	ch      chan rawChange
	mu      sync.Mutex
	entries []ChangeEntry
	dropped int
}

func newChangeLog() *changeLog { return &changeLog{ch: make(chan rawChange, 256)} }

// watchChanges runs under the mirror lock: it only filters and queues.
func (c *Console) watchChanges(ev desk.ChangeEvent) {
	if noisyProps[ev.Name] || ev.Node == "METER" || ev.Old.Kind == rcproto.KindUnknown && ev.New.Kind == rcproto.KindUnknown {
		return
	}
	select {
	case c.changes.ch <- rawChange{at: time.Now(), path: append([]int(nil), ev.Path...), name: ev.Name, node: ev.Node, index: ev.Index, old: ev.Old, new: ev.New}:
	default:
		c.changes.mu.Lock()
		c.changes.dropped++
		c.changes.mu.Unlock()
	}
}

// runChangeLog keeps the queued changes.
func (c *Console) runChangeLog(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case rc := <-c.changes.ch:
			e := ChangeEntry{Time: rc.at.Format("15:04:05.000"), Path: rc.path, Name: rc.name, Index: rc.index, Node: rc.node}
			secret := Secret(e.Node, rc.name)
			e.Old, e.New = encodeValue(rc.old, secret), encodeValue(rc.new, secret)
			e.Text = rc.old.Kind == rcproto.KindString || rc.new.Kind == rcproto.KindString
			c.changes.mu.Lock()
			c.changes.entries = append(c.changes.entries, e)
			if len(c.changes.entries) > changeLogSize {
				c.changes.entries = append(c.changes.entries[:0], c.changes.entries[len(c.changes.entries)-changeLogSize:]...)
			}
			c.changes.mu.Unlock()
		}
	}
}

func encodeValue(v rcproto.Value, secret bool) json.RawMessage {
	if v.Kind == rcproto.KindVoid {
		return json.RawMessage("null")
	}
	var b bytes.Buffer
	desk.WriteValue(&b, v, secret, false)
	return json.RawMessage(b.Bytes())
}

// snapshot returns the entries, oldest first; without text, string values
// become null (for the diagnostics bundle).
func (l *changeLog) snapshot(text bool) ([]ChangeEntry, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ChangeEntry, len(l.entries))
	copy(out, l.entries)
	if !text {
		for i := range out {
			if out[i].Text {
				out[i].Old, out[i].New = json.RawMessage("null"), json.RawMessage("null")
			}
		}
	}
	return out, l.dropped
}

func (c *Console) handleChanges(w http.ResponseWriter, r *http.Request) {
	entries, dropped := c.changes.snapshot(true)
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"changes": entries, "dropped": dropped})
}

func (c *Console) handleChangesClear(w http.ResponseWriter, r *http.Request) {
	c.changes.mu.Lock()
	c.changes.entries, c.changes.dropped = nil, 0
	c.changes.mu.Unlock()
	sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *Console) changesForDiagnostics() []byte {
	entries, dropped := c.changes.snapshot(false)
	raw, _ := json.MarshalIndent(map[string]interface{}{"changes": entries, "dropped": dropped}, "", " ")
	return raw
}
