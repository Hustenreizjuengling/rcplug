package rcproto

import (
	"strconv"
	"strings"
)

// Model identifies the console.
type Model string

const (
	ModelDuo     Model = "duo"
	ModelPro2    Model = "pro2"
	ModelUnknown Model = "unknown"
)

// Profile holds the facts about a console model that the state tree does not
// carry itself. Positions of node families are always discovered from the
// tree (see DiscoverLayout); a profile only adds what cannot be discovered.
type Profile struct {
	Model          Model
	Name           string
	PhysicalFaders int // hardware sliders (FADER 0..n-1); the rest are virtual
	PadsPerBank    int // SMART pads per bank
	PadBanks       int
	// MicInputs and Headphones are the sockets the device has; the state
	// tree has room for four of each on every model.
	MicInputs  int
	Headphones int
	// Controllable offers writes for this model without an explicit opt-in.
	// Models whose behaviour rcplug has not been checked against stay
	// read-only unless the operator overrides it.
	Controllable bool
	// Basis records where the facts come from.
	Basis string
}

// Profiles lists the known models. To add one, see docs/adding-a-model.md.
var Profiles = map[Model]Profile{
	ModelDuo: {
		Model: ModelDuo, Name: "RØDECaster Duo", PhysicalFaders: 4, PadsPerBank: 6, PadBanks: 8, MicInputs: 2, Headphones: 2,
		Controllable: true,
		Basis:        "captured full sync, firmware 1.7.4 (internal/rcsim/fixture); rodecaster-protocol",
	},
	ModelPro2: {
		Model: ModelPro2, Name: "RØDECaster Pro II", PhysicalFaders: 6, PadsPerBank: 8, PadBanks: 8, MicInputs: 4, Headphones: 4,
		Controllable: false,
		Basis:        "rodecaster-protocol and companion-module-rode-rodecaster2; no capture in this repository",
	},
}

// ProfileOf returns the profile of m (a read-only placeholder for unknown models).
func ProfileOf(m Model) Profile {
	if p, ok := Profiles[m]; ok {
		return p
	}
	return Profile{Model: ModelUnknown, Name: "RØDECaster (unknown model)", PadsPerBank: 8, PadBanks: 8}
}

// DetectModel reads SYSTEM.boardType (0 = Pro II, 1 = Duo), falling back to
// SYSTEM.systemName. Anything else is ModelUnknown — never guessed.
func DetectModel(root *Node) Model {
	if root == nil {
		return ModelUnknown
	}
	var sys *Node
	for _, c := range root.Children {
		if c.Type == "SYSTEM" {
			sys = c
			break
		}
	}
	if sys == nil {
		return ModelUnknown
	}
	if bt, ok := sys.Get("boardType"); ok && bt.IsNumber() {
		switch bt.I {
		case 0:
			return ModelPro2
		case 1:
			return ModelDuo
		}
	}
	name, _ := sys.Get("systemName")
	n := strings.ToLower(name.S)
	switch {
	case strings.Contains(n, "duo"):
		return ModelDuo
	case strings.Contains(n, "pro ii") || strings.Contains(n, "pro2") || strings.Contains(n, "pro 2"):
		return ModelPro2
	}
	return ModelUnknown
}

// Name is an addressable entity with a stable id and an English label
// (user interfaces translate by id).
type Name struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Sources are the 30 audio sources in protocol order (channelInputSource
// and INPUTSOURCE ordinal): the 19 of firmware 1.0 (the first MIX matrix
// dimension), the two channels of the internal wireless receiver (1.7.3)
// and the nine sources a RØDECaster Video feeds in sync mode (1.7.6; their
// kind is INPUTSOURCE.inputRcvAudioSourceType).
var Sources = []Name{
	{"combo1", "Mic 1"}, {"combo2", "Mic 2"}, {"combo3", "Mic 3"}, {"combo4", "Mic 4"},
	{"combo1_2", "Mic 1+2"}, {"combo2_3", "Mic 2+3"}, {"combo3_4", "Mic 3+4"},
	{"usb1", "USB 1"}, {"chat", "Chat"}, {"usb2", "USB 2"}, {"bluetooth", "Bluetooth"},
	{"soundpad", "SMART Pads"}, {"game", "Game"}, {"music", "Music"},
	{"virtuala", "Virtual A"}, {"virtualb", "Virtual B"},
	{"callme1", "Call 1"}, {"callme2", "Call 2"}, {"callme3", "Call 3"},
	{"wireless1", "Wireless 1"}, {"wireless2", "Wireless 2"},
	{"sync1", "Sync 1"}, {"sync2", "Sync 2"}, {"sync3", "Sync 3"}, {"sync4", "Sync 4"}, {"sync5", "Sync 5"},
	{"sync6", "Sync 6"}, {"sync7", "Sync 7"}, {"sync8", "Sync 8"}, {"sync9", "Sync 9"},
}

// MixSources is how many sources the MIX matrix's first dimension has on
// the firmware the matrix was decoded on (the 19 of firmware 1.0).
const MixSources = 19

// Outputs are the 13 mix buses in protocol order (second MIX dimension).
var Outputs = []Name{
	{"headphone1", "Headphones 1"}, {"headphone2", "Headphones 2"},
	{"headphone3", "Headphones 3"}, {"headphone4", "Headphones 4"},
	{"speaker", "Speakers"}, {"recording", "Recording"}, {"bluetooth", "Bluetooth"},
	{"usb1", "USB 1"}, {"chat", "Chat"}, {"usb2", "USB 2"},
	{"callme1", "Call 1"}, {"callme2", "Call 2"}, {"callme3", "Call 3"},
}

// IsComboSource reports whether a source is one of the combo (XLR/jack)
// inputs, which have a preamp (gain, phantom power).
func IsComboSource(src int) bool { return src >= 0 && src <= 6 }

// IsCallSource reports whether a source is a CallMe return.
func IsCallSource(src int) bool { return src >= 16 && src <= 18 }

// SourceName returns the source with protocol index v, or a placeholder.
func SourceName(v int64) Name {
	if v >= 0 && int(v) < len(Sources) {
		return Sources[v]
	}
	return Name{ID: "none", Label: "—"}
}

// Faders returns the strip names for the discovered layout.
func (l *Layout) Faders() []Name {
	n := l.Runs["FADER"].Count
	out := make([]Name, 0, n)
	for i := 0; i < n; i++ {
		if i < l.PhysicalFaders {
			out = append(out, Name{ID: "physical" + strconv.Itoa(i+1), Label: "Fader " + strconv.Itoa(i+1)})
		} else {
			v := i - l.PhysicalFaders + 1
			out = append(out, Name{ID: "virtual" + strconv.Itoa(v), Label: "Virtual " + strconv.Itoa(v)})
		}
	}
	return out
}

// UnityFader is the fader position of the 0 dB detent (90 of 127).
const UnityFader = 90.0
