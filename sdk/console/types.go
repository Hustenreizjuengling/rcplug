// Package console is a client for the rcplug console plugin's HTTP API: read
// the desk state (summary, full tree, live events) and write properties.
//
//	c := console.New("")                 // uses sdk.HostURL()
//	s, err := c.Summary(ctx)             // strips, recorder, pads, …
//	err = c.Events(ctx, func(e console.Event) { … })
//
// Writes need the console's control level to allow them (off by default) and
// the PIN if one is set (Client.PIN).
package console

// Summary is the compact, semantic view of the desk (GET api/summary).
type Summary struct {
	Model     string        `json:"model"` // "duo" | "pro2" | "unknown"
	ModelName string        `json:"modelName"`
	Name      string        `json:"name"`
	Firmware  string        `json:"firmware"`
	Show      string        `json:"show"`
	Stale     bool          `json:"stale"` // last known state, not live
	Strips    []Strip       `json:"strips"`
	Monitor   OutputState   `json:"monitor"`
	Bluetooth OutputState   `json:"bluetooth"`
	Recorder  RecorderState `json:"recorder"`
	PadBank   int64         `json:"padBank"`
	Pads      []PadState    `json:"pads"`
}

// Strip is one fader channel.
type Strip struct {
	Index       int     `json:"index"`
	ID          string  `json:"id"`    // physical1… / virtual1…
	Label       string  `json:"label"` // English, e.g. "Fader 1", "Virtual 2"
	Physical    bool    `json:"physical"`
	Source      int64   `json:"source"`      // channelInputSource, -1 = none
	SourceID    string  `json:"sourceId"`    // e.g. "combo1", "usb1"
	SourceLabel string  `json:"sourceLabel"` // English, e.g. "Mic 1"
	Fader       int64   `json:"fader"`       // 0..127
	FaderDb     float64 `json:"faderDb"`     // approximate, relative to the unity detent
	Muted       bool    `json:"muted"`
	Cue         bool    `json:"cue"`
	MeterL      float64 `json:"meterL"`
	MeterR      float64 `json:"meterR"`
}

// OutputState is a level/mute pair (level 0..1).
type OutputState struct {
	Level float64 `json:"level"`
	Muted bool    `json:"muted"`
}

// RecorderState is the multitrack recorder's transport.
type RecorderState struct {
	State  int64  `json:"state"` // 0 ready, 1 paused, 2 recording, 3 no destination
	Label  string `json:"label"`
	TimeMs int64  `json:"timeMs"`
}

// PadState is one SMART pad.
type PadState struct {
	Index  int64  `json:"index"`
	Bank   int64  `json:"bank"`
	Slot   int64  `json:"slot"`
	Name   string `json:"name"`
	Colour int64  `json:"colour"`
	Active bool   `json:"active"`
}

// Status is GET api/status.
type Status struct {
	Connection  Connection `json:"connection"`
	Control     string     `json:"control"` // off | basic | expert
	ReadOnly    bool       `json:"readOnly"`
	PinRequired bool       `json:"pinRequired"`
	Epoch       uint64     `json:"epoch"`
	Seq         uint64     `json:"seq"`
	Stale       bool       `json:"stale"`
	Model       string     `json:"model"`
	ModelName   string     `json:"modelName"`
	Firmware    string     `json:"firmware"`
	Supported   bool       `json:"supported"`
}

// Connection is the console's link to the desk.
type Connection struct {
	State string  `json:"state"` // waiting | connecting | syncing | live | backoff | conflict | stopped
	Code  string  `json:"code,omitempty"`
	Error string  `json:"error,omitempty"`
	Rate  float64 `json:"changesPerSecond"`
}

// Event is one server-sent event from api/events.
type Event struct {
	Name string // hello | changes | resync | status | control
	Data []byte // JSON payload
}
