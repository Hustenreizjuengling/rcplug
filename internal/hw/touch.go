package hw

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"
)

// Touch taps and swipes on the device's touchscreen as a finger would: it
// writes input events into the touchscreen's evdev device, which the device's
// screen app (rc_gui_juce) reads directly (/dev/input/event*). Together with
// the screen mirror this operates every function of the device's screen.
//
// Experimental and off by default: a tap that lands in the wrong place does
// whatever is there, so the console only taps on an explicit request from a
// user who sees the mirrored screen.
type Touch struct {
	Device string // event device; "" = the touchscreen found by its axes

	mu   sync.Mutex // one gesture at a time
	info *TouchInfo // the device found (tests set it)
	id   int32      // tracking ids of our touches
}

// TouchInfo describes the touchscreen.
type TouchInfo struct {
	Device string `json:"device"`
	Name   string `json:"name"`
	XMin   int32  `json:"xMin"`
	XMax   int32  `json:"xMax"`
	YMin   int32  `json:"yMin"`
	YMax   int32  `json:"yMax"`
	MT     bool   `json:"multitouch"` // ABS_MT_POSITION_X/Y (else ABS_X/Y only)
}

// Point is a position in the screen buffer the GUI draws into, 0..1 from the
// top left.
type Point struct{ X, Y float64 }

// BufferPoint turns a point of the mirrored picture (0..1 from its top left),
// which shows the buffer turned by turn degrees clockwise, back into the
// buffer.
func BufferPoint(p Point, turn int) Point {
	switch turn {
	case 90:
		return Point{X: p.Y, Y: 1 - p.X}
	case 180:
		return Point{X: 1 - p.X, Y: 1 - p.Y}
	case 270:
		return Point{X: 1 - p.Y, Y: p.X}
	}
	return p
}

// TouchFlip corrects the touchscreen's orientation if taps land mirrored:
// "", "x", "y" or "xy".
type TouchFlip string

// position maps a buffer point to the touchscreen's coordinates. A
// touchscreen whose long axis lies across the buffer's (the panel reports
// landscape while the GUI draws portrait, or the other way round) has its axes
// swapped.
func (i TouchInfo) position(p Point, portrait bool, flip TouchFlip) (x, y int32) {
	touchPortrait := i.YMax-i.YMin > i.XMax-i.XMin
	if touchPortrait != portrait {
		p = Point{X: p.Y, Y: p.X}
	}
	if flip == "x" || flip == "xy" {
		p.X = 1 - p.X
	}
	if flip == "y" || flip == "xy" {
		p.Y = 1 - p.Y
	}
	clamp := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }
	x = i.XMin + int32(math.Round(clamp(p.X)*float64(i.XMax-i.XMin)))
	y = i.YMin + int32(math.Round(clamp(p.Y)*float64(i.YMax-i.YMin)))
	return x, y
}

// Linux input event codes.
const (
	evSyn         = 0x00
	evKey         = 0x01
	evAbs         = 0x03
	synReport     = 0x00
	btnTouch      = 0x14a
	absX          = 0x00
	absY          = 0x01
	absMTSlot     = 0x2f
	absMTPosX     = 0x35
	absMTPosY     = 0x36
	absMTTracking = 0x39
)

type inputEvent struct {
	typ, code uint16
	value     int32
}

// frames turns a gesture through touch positions into event frames (each
// ends with SYN_REPORT): a press, moves, a release, as a touchscreen driver
// reports them (multi-touch protocol B on slot 0 plus the single-touch axes).
func (i TouchInfo) frames(pts [][2]int32, id int32) [][]inputEvent {
	pos := func(x, y int32) []inputEvent {
		var ev []inputEvent
		if i.MT {
			ev = append(ev, inputEvent{evAbs, absMTSlot, 0}, inputEvent{evAbs, absMTPosX, x}, inputEvent{evAbs, absMTPosY, y})
		}
		return append(ev, inputEvent{evAbs, absX, x}, inputEvent{evAbs, absY, y})
	}
	var out [][]inputEvent
	for k, p := range pts {
		var f []inputEvent
		if k == 0 {
			if i.MT {
				f = append(f, inputEvent{evAbs, absMTSlot, 0}, inputEvent{evAbs, absMTTracking, id})
			}
			f = append(f, inputEvent{evKey, btnTouch, 1})
		}
		f = append(f, pos(p[0], p[1])...)
		out = append(out, append(f, inputEvent{evSyn, synReport, 0}))
	}
	var up []inputEvent
	if i.MT {
		up = append(up, inputEvent{evAbs, absMTSlot, 0}, inputEvent{evAbs, absMTTracking, -1})
	}
	return append(out, append(up, inputEvent{evKey, btnTouch, 0}, inputEvent{evSyn, synReport, 0}))
}

// encode writes events as struct input_event (a zero struct timeval, then
// type, code and value) in the machine's byte order and word size.
func encode(evs []inputEvent) []byte {
	var b bytes.Buffer
	for _, e := range evs {
		if strconv.IntSize == 64 {
			b.Write(make([]byte, 16))
		} else {
			b.Write(make([]byte, 8))
		}
		_ = binary.Write(&b, binary.NativeEndian, e.typ)
		_ = binary.Write(&b, binary.NativeEndian, e.code)
		_ = binary.Write(&b, binary.NativeEndian, e.value)
	}
	return b.Bytes()
}

// Gesture is a tap (one point) or a swipe (several) in buffer coordinates.
type Gesture struct {
	Points   []Point
	Portrait bool          // the buffer is taller than wide
	Flip     TouchFlip     // orientation correction
	Duration time.Duration // press to release (a tap: about 60 ms)
}

// ErrNoTouchscreen: no event device with position axes was found.
var ErrNoTouchscreen = errors.New("no touchscreen found")

// steps spreads a swipe over moves about every 16 ms.
func (g Gesture) steps() []Point {
	if len(g.Points) != 2 || g.Duration <= 0 {
		return g.Points
	}
	n := int(g.Duration / (16 * time.Millisecond))
	if n < 2 {
		n = 2
	}
	if n > 120 {
		n = 120
	}
	a, b := g.Points[0], g.Points[1]
	out := make([]Point, 0, n+1)
	for k := 0; k <= n; k++ {
		f := float64(k) / float64(n)
		out = append(out, Point{X: a.X + (b.X-a.X)*f, Y: a.Y + (b.Y-a.Y)*f})
	}
	return out
}
