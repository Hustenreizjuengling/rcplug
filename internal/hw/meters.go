package hw

import (
	"encoding/binary"
	"errors"
	"math"
)

// The Duo's level meters never travel over the control port. rc_audio_mixer
// publishes them, with the hardware fader positions, in a System V shared
// memory segment (key 0x4D173451, 712 bytes) that the device's screen app
// attaches read-only and polls every 50 ms. The layout below was found by
// analysing the firmware 1.7.6 binaries (see docs/protocol.md):
//
//	0x000  9 fader records of 12 bytes; the int32 at +8 is the position 0..127
//	0x06C 22 meter records of 20 bytes:
//	       u8 stereo, 3 bytes padding, float32 peak L, peak R, level L, level R
//	       (linear, 1.0 = 0 dBFS)
//	0x224 ... counters the mixer keeps for itself
//
// Meter records 0-8 are the channel strips, 9 the master, 10-12 the input,
// output and gain reduction of the channel open on the device's screen, and
// 13-21 each strip's gain reduction (1.0 = none). The tree has a METER node
// for each record, in the same order and with the same stereo flags.
//
// Reading needs no lock (the screen app does not take one either) and cannot
// disturb the mixer: the segment is attached read-only.

// MeterKey is the segment's System V IPC key.
const MeterKey = 0x4D173451

// Layout of the segment.
const (
	MeterSegmentSize = 0x2C8
	FaderRecords     = 9
	MeterRecords     = 22
	faderRecordSize  = 12
	meterOffset      = FaderRecords * faderRecordSize
	meterRecordSize  = 20
)

// ErrNoMeters: the segment does not exist (not a device, or the mixer is not
// running) or has a size this layout does not describe.
var ErrNoMeters = errors.New("no meter segment")

// Meter is one meter record: peak and level per side, linear (1.0 = 0 dBFS).
// A mono meter carries its reading in L.
type Meter struct {
	Stereo         bool
	PeakL, PeakR   float64
	LevelL, LevelR float64
}

// MeterReading is one read of the segment.
type MeterReading struct {
	Faders [FaderRecords]int32 // hardware fader positions 0..127
	Meters [MeterRecords]Meter
}

// parseMeters decodes a copy of the segment. Values the mixer cannot have
// written (NaN, infinite, outside 0..16) read as 0.
func parseMeters(b []byte, r *MeterReading) error {
	if len(b) < MeterSegmentSize {
		return ErrNoMeters
	}
	for i := range r.Faders {
		r.Faders[i] = int32(binary.NativeEndian.Uint32(b[i*faderRecordSize+8:]))
	}
	f := func(off int) float64 {
		v := float64(math.Float32frombits(binary.NativeEndian.Uint32(b[off:])))
		if math.IsNaN(v) || v < 0 || v > 16 {
			return 0
		}
		return v
	}
	for i := range r.Meters {
		o := meterOffset + i*meterRecordSize
		r.Meters[i] = Meter{Stereo: b[o] != 0, PeakL: f(o + 4), PeakR: f(o + 8), LevelL: f(o + 12), LevelR: f(o + 16)}
	}
	return nil
}
