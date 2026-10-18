package console

import (
	"math"
	"strconv"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

// RecordStateLabel names RECORDER.recordState values (English).
func RecordStateLabel(s int64) string {
	switch s {
	case 0:
		return "ready"
	case 1:
		return "paused"
	case 2:
		return "recording"
	case 3:
		return "no destination"
	}
	return "unknown (" + strconv.FormatInt(s, 10) + ")"
}

// FaderDb approximates the dB value of a 0..127 fader position relative to
// the unity detent at 90. The desk publishes no taper; display aid only.
func FaderDb(pos int64) float64 {
	if pos <= 0 {
		return -144
	}
	return 20 * math.Log10(float64(pos)/rcproto.UnityFader)
}

// finite keeps JSON encodable (meters may carry non-finite values).
func finite(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// Summarize builds the compact summary from the tree.
func Summarize(root *rcproto.Node, l *rcproto.Layout, stale bool) *capi.Summary {
	s := &capi.Summary{Model: string(l.Model), ModelName: l.ModelName, Firmware: l.Firmware, Stale: stale}
	get := func(n *rcproto.Node, name string) rcproto.Value {
		if n == nil {
			return rcproto.Value{}
		}
		v, _ := n.Get(name)
		return v
	}
	s.Name = get(l.Node(root, "SYSTEM", 0), "systemName").S
	s.Show = get(l.Node(root, "CURRENTSHOW", 0), "currentShowName").S
	for i, name := range l.Faders() {
		ch := l.Node(root, "CHANNEL", i)
		fd := l.Node(root, "FADER", i)
		src := get(ch, "channelInputSource").I
		sn := rcproto.SourceName(src)
		st := capi.Strip{
			Index: i, ID: name.ID, Label: name.Label, Physical: i < l.PhysicalFaders,
			Source: src, SourceID: sn.ID, SourceLabel: sn.Label,
			Fader: get(fd, "faderLevel").I, Muted: get(ch, "channelOutputMute").Truth(),
			Cue: get(ch, "channelCueEnable").Truth(),
		}
		st.FaderDb = math.Round(FaderDb(st.Fader)*10) / 10
		if p := l.StripMeters.Path(i); p != nil {
			m := root.At(p)
			st.MeterL, st.MeterR = finite(get(m, "meterLevelL").Num()), finite(get(m, "meterLevelR").Num())
		}
		s.Strips = append(s.Strips, st)
	}
	out := l.Node(root, "OUTPUT", 0)
	s.Monitor = capi.OutputState{Level: finite(get(out, "outputMonLevel").Num()), Muted: get(out, "outputMonMute").Truth()}
	s.Bluetooth = capi.OutputState{Level: finite(get(out, "outputBTLevel").Num()), Muted: get(out, "outputBTMute").Truth()}
	rec := l.Node(root, "RECORDER", 0)
	s.Recorder = capi.RecorderState{State: get(rec, "recordState").I, TimeMs: get(rec, "recordTimeMs").I}
	s.Recorder.Label = RecordStateLabel(s.Recorder.State)
	s.PadBank = get(l.Node(root, "GUI", 0), "selectedBank").I
	pads := l.Runs["SOUNDPADS/PAD"]
	per := int64(l.PadsPerBank)
	if per <= 0 {
		per = 8
	}
	for i := 0; i < pads.Count; i++ {
		p := root.At(pads.Path(i))
		idx := get(p, "padIdx").I
		s.Pads = append(s.Pads, capi.PadState{
			Index: idx, Bank: idx / per, Slot: idx % per,
			Name: strings.TrimSpace(get(p, "padName").S), Colour: get(p, "padColourIndex").I,
			Active: get(p, "padActive").Truth(),
		})
	}
	return s
}
