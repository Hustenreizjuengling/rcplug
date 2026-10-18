package hw

import (
	"os"
	"sync"
)

// The Duo's front panel (buttons, encoders, faders) is a microcontroller that
// talks to rc_audio_mixer over the serial port /dev/ttyAML3 using 3-byte,
// MIDI-like messages: two bytes control id, one byte value. Injecting bytes
// into that tty's input queue (TIOCSTI) makes the mixer act exactly as if the
// control had been operated. Technique and codes from phihag/rc2http, which
// documented them on a real Duo.
//
// This is experimental and off by default: an injected message can in rare
// cases interleave with one the panel is sending at the same moment. The codes
// are factual protocol constants as documented by rc2http (which carries no
// licence); they have not been re-verified on hardware by this project.

// PanelControl is one injectable front-panel control.
type PanelControl struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Kind  string `json:"kind"` // button | wheel | fader
	code  [2]byte
}

// PanelControls lists the known Duo controls.
var PanelControls = []PanelControl{
	{"listen1", "Listen 1", "Channel 1", "button", [2]byte{0xb0, 0x19}},
	{"mute1", "Mute 1", "Channel 1", "button", [2]byte{0xb0, 0x1d}},
	{"settings1", "Settings 1", "Channel 1", "button", [2]byte{0xb0, 0x15}},
	{"fader1", "Fader 1", "Channel 1", "fader", [2]byte{0xb0, 0x0f}},
	{"listen2", "Listen 2", "Channel 2", "button", [2]byte{0xb1, 0x19}},
	{"mute2", "Mute 2", "Channel 2", "button", [2]byte{0xb1, 0x1d}},
	{"settings2", "Settings 2", "Channel 2", "button", [2]byte{0xb1, 0x15}},
	{"fader2", "Fader 2", "Channel 2", "fader", [2]byte{0xb1, 0x0f}},
	{"listen3", "Listen 3", "Channel 3", "button", [2]byte{0xb2, 0x19}},
	{"mute3", "Mute 3", "Channel 3", "button", [2]byte{0xb2, 0x1d}},
	{"settings3", "Settings 3", "Channel 3", "button", [2]byte{0xb2, 0x15}},
	{"fader3", "Fader 3", "Channel 3", "fader", [2]byte{0xb2, 0x0f}},
	{"listen4", "Listen 4", "Channel 4", "button", [2]byte{0xb3, 0x19}},
	{"mute4", "Mute 4", "Channel 4", "button", [2]byte{0xb3, 0x1d}},
	{"settings4", "Settings 4", "Channel 4", "button", [2]byte{0xb3, 0x15}},
	{"fader4", "Fader 4", "Channel 4", "fader", [2]byte{0xb3, 0x0f}},
	{"pageLeft", "Page ◀", "Navigation", "button", [2]byte{0xb0, 0x37}},
	{"pageRight", "Page ▶", "Navigation", "button", [2]byte{0xb1, 0x37}},
	{"wheel", "Wheel", "Navigation", "wheel", [2]byte{0xb0, 0x2b}},
	{"wheelPress", "Push wheel", "Navigation", "button", [2]byte{0xb0, 0x2f}},
	{"pad1", "Pad 1 (top left)", "SMART Pads", "button", [2]byte{0xb0, 0x23}},
	{"pad2", "Pad 2 (middle left)", "SMART Pads", "button", [2]byte{0xb1, 0x23}},
	{"pad3", "Pad 3 (bottom left)", "SMART Pads", "button", [2]byte{0xb2, 0x23}},
	{"pad4", "Pad 4 (top right)", "SMART Pads", "button", [2]byte{0xb3, 0x23}},
	{"pad5", "Pad 5 (middle right)", "SMART Pads", "button", [2]byte{0xb4, 0x23}},
	{"pad6", "Pad 6 (bottom right)", "SMART Pads", "button", [2]byte{0xb5, 0x23}},
}

// Panel injects front-panel input into a tty.
type Panel struct {
	TTY string // default /dev/ttyAML3

	mu sync.Mutex // one gesture at a time; never interleave our own bytes
}

func (p *Panel) tty() string {
	if p.TTY == "" {
		return "/dev/ttyAML3"
	}
	return p.TTY
}

// Available reports whether the tty exists.
func (p *Panel) Available() error {
	_, err := os.Stat(p.tty())
	return err
}

func findControl(id string) (PanelControl, bool) {
	for _, c := range PanelControls {
		if c.ID == id {
			return c, true
		}
	}
	return PanelControl{}, false
}

// Each control's code is a 2-byte id followed by the value byte.
func (c PanelControl) bytes(v byte) []byte { return []byte{c.code[0], c.code[1], v} }
