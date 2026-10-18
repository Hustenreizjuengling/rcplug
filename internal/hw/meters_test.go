package hw

import (
	"encoding/binary"
	"math"
	"testing"
)

// segment builds a meter segment as the mixer lays it out.
func segment(faders []int32, meters []Meter) []byte {
	b := make([]byte, MeterSegmentSize)
	for i, f := range faders {
		binary.NativeEndian.PutUint32(b[i*faderRecordSize+8:], uint32(f))
	}
	put := func(off int, v float64) { binary.NativeEndian.PutUint32(b[off:], math.Float32bits(float32(v))) }
	for i, m := range meters {
		o := meterOffset + i*meterRecordSize
		if m.Stereo {
			b[o] = 1
		}
		put(o+4, m.PeakL)
		put(o+8, m.PeakR)
		put(o+12, m.LevelL)
		put(o+16, m.LevelR)
	}
	return b
}

func TestMeterSegmentLayout(t *testing.T) {
	if meterOffset != 0x6C || meterOffset+MeterRecords*meterRecordSize != 0x224 || MeterSegmentSize != 712 {
		t.Fatalf("layout: meters at %#x..%#x of %d", meterOffset, meterOffset+MeterRecords*meterRecordSize, MeterSegmentSize)
	}
	ms := make([]Meter, MeterRecords)
	ms[0] = Meter{Stereo: true, PeakL: 0.5, PeakR: 0.25, LevelL: 0.125, LevelR: 0.0625}
	ms[21] = Meter{PeakL: 1, LevelL: 0.5}
	b := segment([]int32{90, 0, 127}, ms)
	put := func(off int, v float32) { binary.NativeEndian.PutUint32(b[off:], math.Float32bits(v)) }
	put(meterOffset+meterRecordSize+4, float32(math.NaN()))
	put(meterOffset+meterRecordSize+8, float32(math.Inf(1)))
	put(meterOffset+meterRecordSize+12, -1)
	var r MeterReading
	if err := parseMeters(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Faders[0] != 90 || r.Faders[2] != 127 {
		t.Errorf("faders %v", r.Faders)
	}
	if r.Meters[0] != ms[0] || r.Meters[21] != ms[21] {
		t.Errorf("meters %+v %+v", r.Meters[0], r.Meters[21])
	}
	if m := r.Meters[1]; m.PeakL != 0 || m.PeakR != 0 || m.LevelL != 0 {
		t.Errorf("readings the mixer cannot have written must read as 0: %+v", m)
	}
	if err := parseMeters(b[:100], &r); err != ErrNoMeters {
		t.Errorf("short segment: %v", err)
	}
}
