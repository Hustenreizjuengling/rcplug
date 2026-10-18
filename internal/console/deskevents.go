package console

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Desk events: the console turns what happens on the desk into events on the
// host's bus (docs/plugin-api.md#events), so other plugins need not follow
// the whole tree: recording started, paused, stopped and saved, markers,
// mute and listen per channel, pads, the show, the connection.

// deskNames are the properties that make events; everything else (meters
// above all) is dropped at once by the watcher.
var deskNames = map[string]bool{
	"recordState": true, "requestDropMarker": true, "lastRecordingID": true,
	"channelOutputMute": true, "channelCueEnable": true, "padButtonPressed": true,
	"currentShowName": true,
}

type deskChange struct {
	path []int
	name string
	v    rcproto.Value
}

type deskEvents struct {
	ch      chan deskChange
	mu      sync.Mutex
	lastRec int64 // last recordState seen, -1 = none
	live    bool
	failed  bool // a publish failed (logged once)
	publish func(ctx context.Context, typ string, data interface{}) error
}

func newDeskEvents(publish func(ctx context.Context, typ string, data interface{}) error) *deskEvents {
	if publish == nil {
		publish = sdk.Publish
	}
	return &deskEvents{ch: make(chan deskChange, 256), lastRec: -1, publish: publish}
}

// watchDesk runs under the mirror lock: it only filters and queues.
func (c *Console) watchDesk(path []int, name string, v rcproto.Value) {
	if !deskNames[name] {
		return
	}
	select {
	case c.desk.ch <- deskChange{path: append([]int(nil), path...), name: name, v: v}:
	default: // the bus is not keeping up; desk events are best effort
	}
}

// runDeskEvents publishes queued desk changes until ctx ends.
func (c *Console) runDeskEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ch := <-c.desk.ch:
			if typ, data := c.deskEvent(ch); typ != "" {
				c.publishDesk(ctx, typ, data)
			}
		}
	}
}

func (c *Console) publishDesk(ctx context.Context, typ string, data map[string]interface{}) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := c.desk.publish(pctx, typ, data)
	if err == nil || errors.Is(err, sdk.ErrStandalone) {
		return
	}
	c.desk.mu.Lock()
	first := !c.desk.failed
	c.desk.failed = true
	c.desk.mu.Unlock()
	if first {
		c.opts.Logf("console: publishing desk events: %v (further errors not logged)", err)
	}
}

// deskEvent turns one property change into an event type and its data.
func (c *Console) deskEvent(ch deskChange) (string, map[string]interface{}) {
	var nodeType string
	var l *rcproto.Layout
	var root *rcproto.Node
	var recTime int64
	var bank int64
	var stripLabel, stripSource string
	c.mirror.View(func(s desk.Snapshot) {
		if s.Root == nil || s.Layout == nil {
			return
		}
		root, l = s.Root, s.Layout
		if n := s.Root.At(ch.path); n != nil {
			nodeType = n.Type
		}
		if rec := l.Node(s.Root, "RECORDER", 0); rec != nil {
			v, _ := rec.Get("recordTimeMs")
			recTime = v.I
		}
		if gui := l.Node(s.Root, "GUI", 0); gui != nil {
			v, _ := gui.Get("selectedBank")
			bank = v.I
		}
		if i := l.Runs["CHANNEL"].IndexOf(ch.path); i >= 0 {
			if names := l.Faders(); i < len(names) {
				stripLabel = names[i].Label
			}
			if n := l.Node(s.Root, "CHANNEL", i); n != nil {
				src, _ := n.Get("channelInputSource")
				if src.I >= 0 {
					stripSource = rcproto.SourceName(src.I).Label
				}
			}
		}
	})
	if root == nil {
		return "", nil
	}
	switch {
	case nodeType == "RECORDER" && ch.name == "recordState":
		return c.recordingEvent(ch.v.I, recTime)
	case nodeType == "RECORDER" && ch.name == "requestDropMarker" && ch.v.I != 0:
		return "desk.recording.marker", map[string]interface{}{"timeMs": recTime}
	case nodeType == "SYSTEM" && ch.name == "lastRecordingID":
		return "desk.recording.saved", map[string]interface{}{"id": ch.v.I}
	case nodeType == "CHANNEL" && (ch.name == "channelOutputMute" || ch.name == "channelCueEnable"):
		i := l.Runs["CHANNEL"].IndexOf(ch.path)
		if i < 0 {
			return "", nil
		}
		data := map[string]interface{}{"strip": i, "label": stripLabel}
		if stripSource != "" {
			data["source"] = stripSource
		}
		if ch.name == "channelOutputMute" {
			data["muted"] = ch.v.Truth()
			return "desk.channel.mute", data
		}
		data["listening"] = ch.v.Truth()
		return "desk.channel.listen", data
	case nodeType == "PADBUTTON" && ch.name == "padButtonPressed" && ch.v.Truth():
		i := l.Runs["PADBUTTON"].IndexOf(ch.path)
		return "desk.pad.pressed", map[string]interface{}{"pad": i, "bank": bank}
	case nodeType == "CURRENTSHOW" && ch.name == "currentShowName":
		return "desk.show.changed", map[string]interface{}{"name": ch.v.S}
	}
	return "", nil
}

// recordingEvent names a recorder state change: started, resumed, paused or
// stopped, from the state before.
func (c *Console) recordingEvent(state, timeMs int64) (string, map[string]interface{}) {
	c.desk.mu.Lock()
	prev := c.desk.lastRec
	c.desk.lastRec = state
	c.desk.mu.Unlock()
	data := map[string]interface{}{"state": state, "label": RecordStateLabel(state), "timeMs": timeMs}
	switch {
	case state == 2 && prev == 1:
		return "desk.recording.resumed", data
	case state == 2:
		return "desk.recording.started", data
	case state == 1:
		return "desk.recording.paused", data
	case prev == 1 || prev == 2:
		return "desk.recording.stopped", data
	}
	return "desk.recording.state", data
}

// connectionEvent reports when the console gets or loses the desk's state.
func (c *Console) connectionEvent(st desk.Status) {
	live := st.State == desk.StateLive
	c.desk.mu.Lock()
	changed := live != c.desk.live
	c.desk.live = live
	c.desk.mu.Unlock()
	if !changed {
		return
	}
	typ := "desk.connection.lost"
	if live {
		typ = "desk.connection.live"
	}
	go c.publishDesk(context.Background(), typ, map[string]interface{}{"mode": st.Mode, "code": st.Code})
}
