package console

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/hw"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// MeterSource reads the mixer's meter memory (hw.Meters; tests use a fake).
type MeterSource interface {
	Read(r *hw.MeterReading) error
}

// How often the meters are read: as often as the device's screen does while
// a browser is watching, once a second otherwise (the tree and the summary
// stay current), and rarely while there is nothing to read.
var (
	meterFast  = 50 * time.Millisecond
	meterSlow  = time.Second
	meterRetry = 10 * time.Second
)

type meterState struct {
	live   atomic.Bool
	logged atomic.Bool // the first state is logged, then every change
}

func (c *Console) metersLive() bool { return c.meters.live.Load() }

// runMeters copies the mixer's meters and hardware fader positions into the
// tree until ctx ends.
func (c *Console) runMeters(ctx context.Context) {
	if c.opts.Meters == nil {
		return
	}
	var r hw.MeterReading
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next := meterRetry
		err := c.opts.Meters.Read(&r)
		if err == nil {
			ran, applied := false, false
			c.mirror.Overlay(func(s desk.Snapshot) []rcproto.Change {
				ch, ok := meterChanges(s, &r)
				ran, applied = true, ok
				return ch
			})
			switch {
			case !ran: // no tree yet
				t.Reset(meterSlow)
				continue
			case !applied:
				err = errMeterLayout
			case c.mirror.Subscribed():
				next = meterFast
			default:
				next = meterSlow
			}
		}
		live := err == nil
		if c.meters.live.Swap(live) != live || !c.meters.logged.Swap(true) {
			if live {
				c.opts.Logf("console: reading meters from the mixer's shared memory")
			} else {
				c.opts.Logf("console: no meters: %v", err)
			}
		}
		t.Reset(next)
	}
}

var errMeterLayout = errors.New("the tree's meters do not match the mixer's meter memory")

// meterChanges turns a reading into property changes: every METER node's
// levels and stereo flag and the physical FADER nodes' positions. The METER
// run must have one node per meter record; otherwise nothing is applied.
func meterChanges(s desk.Snapshot, r *hw.MeterReading) ([]rcproto.Change, bool) {
	if s.Root == nil || s.Layout == nil {
		return nil, false
	}
	run, ok := s.Layout.Runs["METER"]
	if !ok || run.Count != hw.MeterRecords || s.Layout.StripMeters.Count == 0 {
		return nil, false
	}
	out := make([]rcproto.Change, 0, 4*hw.MeterRecords+hw.FaderRecords)
	set := func(path []int, name string, v rcproto.Value) {
		out = append(out, rcproto.Change{Type: rcproto.PropertyChanged, Path: path, Name: name, Value: v})
	}
	for k, m := range r.Meters {
		p := run.Path(k)
		peakR, levelR := m.PeakR, m.LevelR
		if !m.Stereo {
			peakR, levelR = m.PeakL, m.LevelL // a mono meter reads the same on both sides
		}
		set(p, "meterStereo", rcproto.Bool(m.Stereo))
		set(p, "meterPeakL", meterValue(m.PeakL))
		set(p, "meterLevelL", meterValue(m.LevelL))
		set(p, "meterPeakR", meterValue(peakR))
		set(p, "meterLevelR", meterValue(levelR))
	}
	// The mixer keeps the hardware faders' positions here; over the control
	// port a move only shows in the mix levels.
	for i := 0; i < s.Layout.PhysicalFaders && i < hw.FaderRecords; i++ {
		p := s.Layout.Path("FADER", i)
		n := s.Root.At(p)
		if n == nil {
			continue
		}
		lo, hi := int64(0), int64(127)
		if v, ok := n.Get("faderMin"); ok {
			lo = v.I
		}
		if v, ok := n.Get("faderMax"); ok {
			hi = v.I
		}
		if v := int64(r.Faders[i]); v >= lo && v <= hi {
			set(p, "faderLevel", rcproto.Int(v))
		}
	}
	return out, true
}

// meterValue rounds a linear reading to four decimals (about 0.1 dB at
// -40 dBFS): finer steps only add traffic.
func meterValue(v float64) rcproto.Value {
	return rcproto.Double(math.Round(v*1e4) / 1e4)
}
