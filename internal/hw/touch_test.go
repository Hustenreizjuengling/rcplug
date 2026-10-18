package hw

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func near(a, b Point) bool { return math.Abs(a.X-b.X) < 1e-9 && math.Abs(a.Y-b.Y) < 1e-9 }

// The Duo's mirror shows its portrait buffer turned by 270°: the clock at the
// top left of the picture is at the top right of the buffer.
func TestBufferPointUndoesTheMirrorsTurn(t *testing.T) {
	for _, c := range []struct {
		turn    int
		in, out Point
	}{
		{0, Point{0.1, 0.2}, Point{0.1, 0.2}},
		{270, Point{0.1, 0.1}, Point{0.9, 0.1}},
		{270, Point{0.9, 0.9}, Point{0.1, 0.9}},
		{90, Point{0.1, 0.1}, Point{0.1, 0.9}},
		{180, Point{0.25, 0.75}, Point{0.75, 0.25}},
	} {
		if got := BufferPoint(c.in, c.turn); !near(got, c.out) {
			t.Errorf("BufferPoint(%v, %d) = %v, want %v", c.in, c.turn, got, c.out)
		}
	}
	// Turning the buffer as the screen mirror does and back gives the point again.
	src := Point{0.3, 0.8}
	for _, turn := range []int{90, 180, 270} {
		img := image2(src, turn)
		if got := BufferPoint(img, turn); !near(got, src) {
			t.Errorf("%d°: %v -> %v -> %v", turn, src, img, got)
		}
	}
}

// image2 is where rotate() puts a buffer point (normalised).
func image2(p Point, turn int) Point {
	switch turn {
	case 90:
		return Point{X: 1 - p.Y, Y: p.X}
	case 180:
		return Point{X: 1 - p.X, Y: 1 - p.Y}
	case 270:
		return Point{X: p.Y, Y: 1 - p.X}
	}
	return p
}

func TestTouchPositionSwapsAndFlips(t *testing.T) {
	portraitTouch := TouchInfo{XMin: 0, XMax: 719, YMin: 0, YMax: 1279, MT: true}
	landscapeTouch := TouchInfo{XMin: 0, XMax: 1279, YMin: 0, YMax: 719, MT: true}
	p := Point{0.25, 0.75}
	if x, y := portraitTouch.position(p, true, ""); x != 180 || y != 959 {
		t.Errorf("portrait on portrait: %d,%d", x, y)
	}
	if x, y := landscapeTouch.position(p, true, ""); x != 959 || y != 180 {
		t.Errorf("landscape touch on a portrait buffer: %d,%d", x, y)
	}
	if x, y := portraitTouch.position(p, true, "xy"); x != 539 || y != 320 {
		t.Errorf("flipped: %d,%d", x, y)
	}
	if x, y := portraitTouch.position(Point{-1, 2}, true, ""); x != 0 || y != 1279 {
		t.Errorf("clamped: %d,%d", x, y)
	}
}

type decoded struct {
	typ, code uint16
	value     int32
}

func decode(t *testing.T, raw []byte) []decoded {
	t.Helper()
	size := 24
	if strconv.IntSize == 32 {
		size = 16
	}
	if len(raw)%size != 0 {
		t.Fatalf("%d bytes is not a whole number of events", len(raw))
	}
	var out []decoded
	for off := 0; off < len(raw); off += size {
		var d decoded
		r := bytes.NewReader(raw[off+size-8 : off+size])
		_ = binary.Read(r, binary.NativeEndian, &d.typ)
		_ = binary.Read(r, binary.NativeEndian, &d.code)
		_ = binary.Read(r, binary.NativeEndian, &d.value)
		out = append(out, d)
	}
	return out
}

// A tap is a press and a release as a multi-touch driver reports them.
func TestTapWritesPressAndRelease(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("writes an event device")
	}
	dev := filepath.Join(t.TempDir(), "event0")
	_ = os.WriteFile(dev, nil, 0o644)
	tc := &Touch{info: &TouchInfo{Device: dev, XMin: 0, XMax: 719, YMin: 0, YMax: 1279, MT: true}}
	start := time.Now()
	if err := tc.Do(Gesture{Points: []Point{{0.5, 0.25}}, Portrait: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("press and release without a pause")
	}
	raw, _ := os.ReadFile(dev)
	ev := decode(t, raw)
	want := []decoded{
		{evAbs, absMTSlot, 0}, {evAbs, absMTTracking, ev[1].value}, {evKey, btnTouch, 1},
		{evAbs, absMTSlot, 0}, {evAbs, absMTPosX, 360}, {evAbs, absMTPosY, 320}, {evAbs, absX, 360}, {evAbs, absY, 320}, {evSyn, synReport, 0},
		{evAbs, absMTSlot, 0}, {evAbs, absMTTracking, -1}, {evKey, btnTouch, 0}, {evSyn, synReport, 0},
	}
	if len(ev) != len(want) {
		t.Fatalf("%d events: %+v", len(ev), ev)
	}
	for k := range want {
		if ev[k] != want[k] {
			t.Errorf("event %d = %+v, want %+v", k, ev[k], want[k])
		}
	}
	if ev[1].value < 0 {
		t.Errorf("tracking id %d", ev[1].value)
	}
}

// A swipe moves from start to end in steps, then releases.
func TestSwipeMovesInSteps(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("writes an event device")
	}
	dev := filepath.Join(t.TempDir(), "event0")
	_ = os.WriteFile(dev, nil, 0o644)
	tc := &Touch{info: &TouchInfo{Device: dev, XMin: 0, XMax: 1000, YMin: 0, YMax: 2000, MT: false}}
	if err := tc.Do(Gesture{Points: []Point{{0.1, 0.5}, {0.9, 0.5}}, Portrait: true, Duration: 200 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dev)
	var xs []int32
	for _, e := range decode(t, raw) {
		if e.typ == evAbs && e.code == absMTPosX {
			t.Fatal("multi-touch events on a single-touch device")
		}
		if e.typ == evAbs && e.code == absX {
			xs = append(xs, e.value)
		}
	}
	if len(xs) < 5 || xs[0] != 100 || xs[len(xs)-1] != 900 {
		t.Fatalf("x positions %v", xs)
	}
	for k := 1; k < len(xs); k++ {
		if xs[k] < xs[k-1] {
			t.Fatalf("not monotonic: %v", xs)
		}
	}
}
