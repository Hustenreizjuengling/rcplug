package rcproto_test

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

// Known wire vectors (locked against the reference implementation).
func TestValueKnownBytes(t *testing.T) {
	cases := []struct {
		v    rcproto.Value
		want []byte
	}{
		{rcproto.Bool(true), []byte{0x01, 0x01, 0x02}},
		{rcproto.Bool(false), []byte{0x01, 0x01, 0x03}},
		{rcproto.Int(75), []byte{0x01, 0x05, 0x01, 0x4b, 0, 0, 0}},
		{rcproto.Int(-1), []byte{0x01, 0x05, 0x01, 0xff, 0xff, 0xff, 0xff}},
		{rcproto.Binary(rcproto.TriggerPress), []byte{0x01, 0x07, 0x08, 1, 1, 2, 1, 1, 2}},
		{rcproto.Double(1.0), []byte{0x01, 0x09, 0x04, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f}},
		{rcproto.String("ab"), []byte{0x01, 0x04, 0x05, 'a', 'b', 0}},
		{rcproto.Void(), []byte{0x00}},
	}
	for _, c := range cases {
		got := c.v.AppendTo(nil)
		if !bytes.Equal(got, c.want) {
			t.Errorf("%v: got % x, want % x", c.v, got, c.want)
		}
	}
}

func TestValueRoundTrip(t *testing.T) {
	vals := []rcproto.Value{
		rcproto.Bool(true), rcproto.Bool(false), rcproto.Int(0), rcproto.Int(math.MinInt32),
		rcproto.Int64(1 << 40), rcproto.Int64(-5), rcproto.Double(0.472441), rcproto.Double(-3e9),
		rcproto.String(""), rcproto.String("0.472441|0.472441"), rcproto.String("RØDE ✓"),
		rcproto.Binary([]byte{}), rcproto.Binary(bytes.Repeat([]byte{7}, 300)),
		rcproto.Array(rcproto.Int(1), rcproto.String("x"), rcproto.Array(rcproto.Bool(true))),
		rcproto.Void(),
	}
	for _, v := range vals {
		// Wrap in a property-changed frame to exercise the public decoder.
		frame := rcproto.EncodePropertyChanged([]int{3, 300, 70000}, "p", v)
		c, err := rcproto.DecodeChange(frame)
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if !c.Value.Equal(v) {
			t.Errorf("round trip %v -> %v", v, c.Value)
		}
		if got := c.Path; len(got) != 3 || got[0] != 3 || got[1] != 300 || got[2] != 70000 {
			t.Errorf("path %v", got)
		}
	}
}

func TestDecodeKnownChangeFrames(t *testing.T) {
	// faderLevel = 75 at path [0, 7] (from the reference crate's tests).
	b := []byte{0x01, 0x01, 0x02, 0x00, 0x01, 0x07,
		'f', 'a', 'd', 'e', 'r', 'L', 'e', 'v', 'e', 'l', 0x00,
		0x01, 0x05, 0x01, 0x4b, 0, 0, 0}
	c, err := rcproto.DecodeChange(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != rcproto.PropertyChanged || c.Name != "faderLevel" || c.Value.I != 75 ||
		len(c.Path) != 2 || c.Path[0] != 0 || c.Path[1] != 7 {
		t.Fatalf("decoded %+v", c)
	}
	if !bytes.Equal(c.Encode(), b) {
		t.Fatalf("re-encode mismatch")
	}
	// childMoved path=[2] old=1 new=4
	c, err = rcproto.DecodeChange([]byte{0x05, 0x01, 0x01, 0x01, 0x02, 0x01, 0x01, 0x01, 0x04})
	if err != nil || c.Type != rcproto.ChildMoved || c.Index != 1 || c.NewIndex != 4 {
		t.Fatalf("childMoved: %+v %v", c, err)
	}
	// Trailing garbage and unknown types are rejected.
	if _, err := rcproto.DecodeChange(append(b, 0)); err == nil {
		t.Error("trailing byte accepted")
	}
	if _, err := rcproto.DecodeChange([]byte{0xff}); err == nil {
		t.Error("unknown change type accepted")
	}
	if _, err := rcproto.DecodeChange(b[:len(b)-2]); err == nil {
		t.Error("truncated frame accepted")
	}
}

func duoBody(t *testing.T) []byte {
	t.Helper()
	body, n, err := rcproto.ParseFrame(rcsim.DuoFrame)
	if err != nil || n != len(rcsim.DuoFrame) {
		t.Fatalf("fixture frame: n=%d err=%v", n, err)
	}
	return body
}

// The captured Duo full sync must decode completely and re-encode to the
// identical bytes — the strongest check that the codec matches the device.
func TestDuoFullSyncRoundTrip(t *testing.T) {
	body := duoBody(t)
	c, err := rcproto.DecodeChange(body)
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != rcproto.FullSync || c.Tree.Type != "Rodecaster" {
		t.Fatalf("got %s root %q", c.Type, c.Tree.Type)
	}
	nodes, props := c.Tree.Count()
	if nodes != 870 || props != 6923 { // manifest.toml of the capture
		t.Errorf("nodes=%d props=%d, want 870/6923", nodes, props)
	}
	if got := c.Encode(); !bytes.Equal(got, body) {
		t.Fatalf("re-encoded full sync differs (%d vs %d bytes)", len(got), len(body))
	}
}

func TestDuoLayout(t *testing.T) {
	root, err := rcsim.DuoTree()
	if err != nil {
		t.Fatal(err)
	}
	l := rcproto.DiscoverLayout(root)
	if l.Model != rcproto.ModelDuo || l.Firmware != "1.7.4" {
		t.Errorf("model=%s fw=%s", l.Model, l.Firmware)
	}
	want := map[string]int{
		"CHANNEL": 10, "FADER": 9, "METER": 22, "POT": 4, "MIX": 247, "INPUTSOURCE": 19,
		"HEADPHONE": 4, "EFFECTS_PARAMETERS": 11, "MIXMINUSES": 13, "SOUNDPADS/PAD": 21,
		"SIPCALLSLOTS": 3, "SIPCALLING/SIPREGISTRATION": 2, "SYSTEM/STORAGEVOLUME": 3,
		"PADEFFECTS/EFFECTS_PARAMETERS": 7, "SHOWS/SHOW": 7,
	}
	for fam, n := range want {
		if got := l.Runs[fam].Count; got != n {
			t.Errorf("%s: %d, want %d", fam, got, n)
		}
	}
	if l.SourceCount != 19 || l.PhysicalFaders != 4 || l.VirtualFaders != 5 || l.PadsPerBank != 6 {
		t.Errorf("sources=%d phys=%d virt=%d pads/bank=%d", l.SourceCount, l.PhysicalFaders, l.VirtualFaders, l.PadsPerBank)
	}
	if l.StripMeters.Count != 9 || l.OutputMeters.Count != 13 {
		t.Errorf("meters strip=%d out=%d", l.StripMeters.Count, l.OutputMeters.Count)
	}
	// CHANNEL is a root run; FADER sits under PHYSICALINTERFACE.
	if p := l.Path("CHANNEL", 0); len(p) != 1 || root.At(p).Type != "CHANNEL" {
		t.Errorf("channel path %v", p)
	}
	if p := l.Path("FADER", 8); len(p) != 2 || root.At(p).Type != "FADER" {
		t.Errorf("fader path %v", p)
	}
	if n := root.At(l.MixPath(18, 12)); n == nil || n.Type != "MIX" {
		t.Errorf("last mix cell missing")
	}
	if l.MixPath(19, 0) != nil || l.MixPath(0, 13) != nil {
		t.Errorf("out-of-range mix path accepted")
	}
	names := l.Faders()
	if len(names) != 9 || names[3].Label != "Fader 4" || names[4].Label != "Virtual 1" {
		t.Errorf("fader names %v", names)
	}
}

func TestApplyChanges(t *testing.T) {
	root, _ := rcsim.DuoTree()
	l := rcproto.DiscoverLayout(root)
	path := l.Path("CHANNEL", 2)
	r, err := rcproto.Apply(root, &rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: "channelOutputMute", Value: rcproto.Bool(true)})
	if err != nil || r != root {
		t.Fatal(err)
	}
	if v, _ := root.At(path).Get("channelOutputMute"); !v.Truth() {
		t.Error("mute not applied")
	}
	if _, err := rcproto.Apply(root, &rcproto.Change{Type: rcproto.PropertyChanged, Path: []int{9999}, Name: "x"}); err == nil {
		t.Error("write to missing node accepted")
	}
	// Structural: add, move, remove under SOUNDPADS.
	pads := l.Runs["SOUNDPADS/PAD"]
	parent := pads.Parent
	before := len(root.At(parent).Children)
	add := &rcproto.Change{Type: rcproto.ChildAdded, Path: parent, Index: 0, Tree: &rcproto.Node{Type: "PAD"}}
	if _, err := rcproto.Apply(root, add); err != nil {
		t.Fatal(err)
	}
	if got := len(root.At(parent).Children); got != before+1 || len(root.At(parent).Children[0].Props) != 0 {
		t.Fatalf("childAdded: %d", got)
	}
	if _, err := rcproto.Apply(root, &rcproto.Change{Type: rcproto.ChildMoved, Path: parent, Index: 0, NewIndex: 3}); err != nil {
		t.Fatal(err)
	}
	if len(root.At(parent).Children[3].Props) != 0 {
		t.Fatal("childMoved: wrong node at new index")
	}
	if _, err := rcproto.Apply(root, &rcproto.Change{Type: rcproto.ChildRemoved, Path: parent, Index: 3}); err != nil {
		t.Fatal(err)
	}
	if got := len(root.At(parent).Children); got != before {
		t.Fatalf("childRemoved: %d", got)
	}
}

// slowReader returns at most n bytes per Read to exercise frame reassembly.
type slowReader struct {
	r io.Reader
	n int
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(p) > s.n {
		p = p[:s.n]
	}
	return s.r.Read(p)
}

func TestFrameReaderResync(t *testing.T) {
	a := rcproto.Frame(rcproto.EncodePropertyChanged([]int{1}, "a", rcproto.Int(1)))
	b := rcproto.Frame(rcproto.EncodePropertyChanged([]int{2}, "b", rcproto.Bool(true)))
	var stream []byte
	stream = append(stream, 0xde, 0xad, 0xbe) // garbage
	stream = append(stream, a...)
	stream = append(stream, b...)
	stream = append(stream, rcsim.DuoFrame...)
	fr := rcproto.NewFrameReader(&slowReader{r: bytes.NewReader(stream), n: 7})
	var names []string
	for {
		body, err := fr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		c, err := rcproto.DecodeChange(body)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, c.Type.String()+":"+c.Name)
	}
	if len(names) != 3 || names[0] != "propertyChanged:a" || names[1] != "propertyChanged:b" || names[2] != "fullSync:" {
		t.Fatalf("frames %v", names)
	}
	if fr.Resyncs != 3 {
		t.Errorf("resyncs %d, want 3", fr.Resyncs)
	}
}

func FuzzDecodeChange(f *testing.F) {
	f.Add(rcproto.EncodePropertyChanged([]int{0, 7}, "faderLevel", rcproto.Int(75)))
	f.Add([]byte{0x02, 'R', 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := rcproto.DecodeChange(b)
		if err != nil {
			return
		}
		// Anything that decodes must re-decode identically after encoding.
		c2, err := rcproto.DecodeChange(c.Encode())
		if err != nil || c2.Type != c.Type || c2.Name != c.Name {
			t.Fatalf("unstable round trip: %v", err)
		}
	})
}

func TestSanitize(t *testing.T) {
	root := &rcproto.Node{Type: "ROOT", Children: []*rcproto.Node{{
		Type: "NETWORK",
		Props: []rcproto.Property{
			{Name: "wifiPSK", Value: rcproto.String("hunter2")},
			{Name: "wifiSSID", Value: rcproto.String("Home")},
			{Name: "blob", Value: rcproto.Binary([]byte{1, 2, 3})},
			{Name: "list", Value: rcproto.Array(rcproto.String("a"), rcproto.Int(5))},
			{Name: "ipCount", Value: rcproto.Int(3)},
			{Name: "enabled", Value: rcproto.Bool(true)},
		},
	}, {
		Type:  "SYSTEM",
		Props: []rcproto.Property{{Name: "systemFirmwareVersion", Value: rcproto.String("1.2.3")}, {Name: "boardType", Value: rcproto.Int(1)}},
	}}}
	s := rcproto.Sanitize(root)
	net := s.Children[0]
	for _, name := range []string{"wifiPSK", "wifiSSID"} {
		if v, _ := net.Get(name); v.S != rcproto.Redacted {
			t.Errorf("%s = %q, want redacted", name, v.S)
		}
	}
	if v, _ := net.Get("blob"); !bytes.Equal(v.B, []byte{0, 0, 0}) {
		t.Errorf("blob = %v, want zeroed with the same length", v.B)
	}
	if v, _ := net.Get("list"); v.A[0].S != rcproto.Redacted || v.A[1].I != 5 {
		t.Errorf("list = %v", v)
	}
	if v, _ := net.Get("ipCount"); v.I != 3 {
		t.Errorf("numbers must be kept, got %v", v)
	}
	if v, _ := s.Children[1].Get("systemFirmwareVersion"); v.S != "1.2.3" {
		t.Errorf("firmware version must be kept, got %q", v.S)
	}
	if v, _ := root.Children[0].Get("wifiPSK"); v.S != "hunter2" {
		t.Error("rcproto.Sanitize modified its input")
	}
	again := rcproto.Sanitize(s)
	if !bytes.Equal(again.AppendTo(nil), s.AppendTo(nil)) {
		t.Error("rcproto.Sanitize is not idempotent")
	}
	if rcproto.DetectModel(s) != rcproto.ModelDuo {
		t.Error("model detection must survive sanitizing")
	}
}
