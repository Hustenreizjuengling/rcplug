// Package rcproto implements the RØDECaster Pro II / Duo control protocol: the
// JUCE ValueTree that rc_audio_mixer exposes on TCP port 2345, synchronised
// with juce::ValueTreeSynchroniser change frames.
//
// The package is pure (no I/O): value and tree codecs, change frames, TCP
// framing, and discovery of where the device's node families live in the tree.
// Wire knowledge comes from the MIT-licensed rodecaster-protocol crate
// (github.com/Yeradon/rodecaster-protocol), cross-checked against JUCE's
// juce_Variant.cpp / juce_ValueTreeSynchroniser.cpp and a captured Duo full sync.
package rcproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Kind is the juce::var stream marker that follows a value's length frame.
type Kind uint8

// juce::var VariantStreamMarkers. Note true = 2 and false = 3.
const (
	KindVoid      Kind = 0 // empty length frame or marker 9
	KindInt       Kind = 1 // int32, 4 bytes LE
	KindBoolTrue  Kind = 2
	KindBoolFalse Kind = 3
	KindDouble    Kind = 4 // 8 bytes IEEE 754 LE
	KindString    Kind = 5 // UTF-8 + NUL
	KindInt64     Kind = 6 // 8 bytes LE
	KindArray     Kind = 7 // compressed count, then values
	KindBinary    Kind = 8 // raw bytes (rest of the frame)
	KindUndefined Kind = 9
	// KindUnknown marks an unrecognised marker; Value.Marker holds the byte.
	KindUnknown Kind = 0xff
)

// Value is a decoded juce::var. Bools are normalised to KindBoolTrue/False.
type Value struct {
	Kind Kind
	I    int64   // KindInt, KindInt64
	F    float64 // KindDouble
	S    string  // KindString
	B    []byte  // KindBinary, or raw payload of an unknown marker
	A    []Value // KindArray
	// Marker holds the raw marker byte of an unrecognised type so it can be
	// re-encoded unchanged.
	Marker byte
}

// Constructors.
func Int(v int64) Value      { return Value{Kind: KindInt, I: v} }
func Int64(v int64) Value    { return Value{Kind: KindInt64, I: v} }
func Double(v float64) Value { return Value{Kind: KindDouble, F: v} }
func String(v string) Value  { return Value{Kind: KindString, S: v} }
func Binary(v []byte) Value  { return Value{Kind: KindBinary, B: v} }
func Array(v ...Value) Value { return Value{Kind: KindArray, A: v} }
func Void() Value            { return Value{Kind: KindVoid} }
func Bool(v bool) Value {
	if v {
		return Value{Kind: KindBoolTrue}
	}
	return Value{Kind: KindBoolFalse}
}

// IsBool reports whether v is a boolean.
func (v Value) IsBool() bool { return v.Kind == KindBoolTrue || v.Kind == KindBoolFalse }

// Truth returns the boolean value (false for non-bools).
func (v Value) Truth() bool { return v.Kind == KindBoolTrue }

// IsNumber reports whether v is an int, int64 or double.
func (v Value) IsNumber() bool {
	return v.Kind == KindInt || v.Kind == KindInt64 || v.Kind == KindDouble
}

// Num returns the value as float64 (bools as 0/1, non-numbers as 0).
func (v Value) Num() float64 {
	switch v.Kind {
	case KindInt, KindInt64:
		return float64(v.I)
	case KindDouble:
		return v.F
	case KindBoolTrue:
		return 1
	}
	return 0
}

// TypeCode is a one-letter type tag used by the JSON API:
// i int, l int64, b bool, d double, s string, a array, x binary, v void, u unknown.
func (v Value) TypeCode() byte {
	switch v.Kind {
	case KindInt:
		return 'i'
	case KindInt64:
		return 'l'
	case KindBoolTrue, KindBoolFalse:
		return 'b'
	case KindDouble:
		return 'd'
	case KindString:
		return 's'
	case KindArray:
		return 'a'
	case KindBinary:
		return 'x'
	case KindVoid, KindUndefined:
		return 'v'
	}
	return 'u'
}

// Equal reports deep equality.
func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind {
		return false
	}
	switch v.Kind {
	case KindInt, KindInt64:
		return v.I == o.I
	case KindDouble:
		return v.F == o.F || (math.IsNaN(v.F) && math.IsNaN(o.F))
	case KindString:
		return v.S == o.S
	case KindBinary:
		return string(v.B) == string(o.B)
	case KindArray:
		if len(v.A) != len(o.A) {
			return false
		}
		for i := range v.A {
			if !v.A[i].Equal(o.A[i]) {
				return false
			}
		}
		return true
	case KindBoolTrue, KindBoolFalse, KindVoid, KindUndefined:
		return true
	}
	return v.Marker == o.Marker && string(v.B) == string(o.B)
}

func (v Value) String() string {
	switch v.Kind {
	case KindInt, KindInt64:
		return strconv.FormatInt(v.I, 10)
	case KindBoolTrue:
		return "true"
	case KindBoolFalse:
		return "false"
	case KindDouble:
		return strconv.FormatFloat(v.F, 'g', -1, 64)
	case KindString:
		return strconv.Quote(v.S)
	case KindBinary:
		return fmt.Sprintf("bin(%x)", v.B)
	case KindArray:
		s := "["
		for i, e := range v.A {
			if i > 0 {
				s += ","
			}
			s += e.String()
		}
		return s + "]"
	case KindVoid, KindUndefined:
		return "void"
	}
	return fmt.Sprintf("unknown(%d:%x)", v.Marker, v.B)
}

// ErrShort is returned when a buffer ends inside a value.
var ErrShort = errors.New("rcproto: truncated data")

// reader is a cursor over a byte slice.
type reader struct {
	b     []byte
	pos   int
	depth int  // arrays within arrays
	left  *int // elements the message may still hold (shared with nested readers)
}

// maxElements bounds what one message decodes into (nodes, properties,
// array elements together): a Duo's full sync has about 10 000, and a
// hostile count cannot make the decoder allocate hundreds of MB on a device
// with little memory to spare.
const maxElements = 250000

// spend takes n elements from the message's budget.
func (r *reader) spend(n int) error {
	if r.left == nil {
		v := maxElements
		r.left = &v
	}
	if n > *r.left {
		return fmt.Errorf("rcproto: more than %d elements in one message", maxElements)
	}
	*r.left -= n
	return nil
}

// capHint is a slice capacity for a count: what is decoded grows the slice,
// not what the count claims.
func capHint(n int) int { return min(n, 1024) }

func (r *reader) remaining() int { return len(r.b) - r.pos }

func (r *reader) u8() (byte, error) {
	if r.pos >= len(r.b) {
		return 0, ErrShort
	}
	c := r.b[r.pos]
	r.pos++
	return c, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, ErrShort
	}
	s := r.b[r.pos : r.pos+n]
	r.pos += n
	return s, nil
}

// compressedInt reads juce::InputStream::readCompressedInt: a size byte (low 7
// bits = number of value bytes, at most 4; high bit = negative) followed by
// that many little-endian bytes.
func (r *reader) compressedInt() (int64, error) {
	sb, err := r.u8()
	if err != nil {
		return 0, err
	}
	n := int(sb & 0x7f)
	if n > 4 {
		return 0, fmt.Errorf("rcproto: corrupt compressed int (size %d)", n)
	}
	raw, err := r.bytes(n)
	if err != nil {
		return 0, err
	}
	var v int64
	for i, c := range raw {
		v |= int64(c) << (8 * i)
	}
	if sb&0x80 != 0 {
		v = -v
	}
	return v, nil
}

// count reads a non-negative compressed int bounded by the bytes left, which
// protects against absurd pre-allocations from corrupt input.
func (r *reader) count() (int, error) {
	v, err := r.compressedInt()
	if err != nil {
		return 0, err
	}
	if v < 0 || v > int64(r.remaining()) {
		return 0, fmt.Errorf("rcproto: implausible count %d", v)
	}
	if err := r.spend(int(v)); err != nil {
		return 0, err
	}
	return int(v), nil
}

// cstring reads a NUL-terminated UTF-8 string.
func (r *reader) cstring() (string, error) {
	for i := r.pos; i < len(r.b); i++ {
		if r.b[i] == 0 {
			s := string(r.b[r.pos:i])
			r.pos = i + 1
			return s, nil
		}
	}
	return "", ErrShort
}

// value reads one juce::var (var::readFromStream).
func (r *reader) value() (Value, error) {
	n, err := r.compressedInt()
	if err != nil {
		return Value{}, err
	}
	if n <= 0 {
		return Void(), nil
	}
	if n > int64(r.remaining()) {
		return Value{}, ErrShort
	}
	marker, _ := r.u8()
	plen := int(n - 1)
	switch Kind(marker) {
	case KindInt:
		b, err := r.bytes(plen)
		if err != nil || len(b) < 4 {
			return Value{}, ErrShort
		}
		// JUCE always writes exactly 4 bytes; extra payload is ignored.
		return Int(int64(int32(binary.LittleEndian.Uint32(b)))), nil
	case KindBoolTrue:
		_, err := r.bytes(plen)
		return Bool(true), err
	case KindBoolFalse:
		_, err := r.bytes(plen)
		return Bool(false), err
	case KindDouble:
		b, err := r.bytes(plen)
		if err != nil || len(b) < 8 {
			return Value{}, ErrShort
		}
		return Double(math.Float64frombits(binary.LittleEndian.Uint64(b))), nil
	case KindString:
		b, err := r.bytes(plen)
		if err != nil {
			return Value{}, err
		}
		for len(b) > 0 && b[len(b)-1] == 0 {
			b = b[:len(b)-1]
		}
		return String(string(b)), nil
	case KindInt64:
		b, err := r.bytes(plen)
		if err != nil || len(b) < 8 {
			return Value{}, ErrShort
		}
		return Int64(int64(binary.LittleEndian.Uint64(b))), nil
	case KindArray:
		body, err := r.bytes(plen)
		if err != nil {
			return Value{}, err
		}
		if r.depth >= maxDepth {
			return Value{}, fmt.Errorf("rcproto: arrays nested too deep")
		}
		if err := r.spend(0); err != nil { // the budget nested readers share
			return Value{}, err
		}
		sub := &reader{b: body, depth: r.depth + 1, left: r.left}
		cnt, err := sub.count()
		if err != nil {
			return Value{}, err
		}
		arr := make([]Value, 0, capHint(cnt))
		for i := 0; i < cnt; i++ {
			e, err := sub.value()
			if err != nil {
				return Value{}, err
			}
			arr = append(arr, e)
		}
		return Array(arr...), nil
	case KindBinary:
		b, err := r.bytes(plen)
		if err != nil {
			return Value{}, err
		}
		return Binary(append([]byte(nil), b...)), nil
	case KindUndefined:
		_, err := r.bytes(plen)
		return Value{Kind: KindUndefined}, err
	default:
		b, err := r.bytes(plen)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindUnknown, Marker: marker, B: append([]byte(nil), b...)}, nil
	}
}

// appendCompressedInt writes juce::OutputStream::writeCompressedInt.
func appendCompressedInt(out []byte, v int64) []byte {
	neg := v < 0
	m := uint64(v)
	if neg {
		m = uint64(-v)
	}
	var buf [4]byte
	n := 0
	for m != 0 && n < 4 {
		buf[n] = byte(m)
		m >>= 8
		n++
	}
	sb := byte(n)
	if neg {
		sb |= 0x80
	}
	out = append(out, sb)
	return append(out, buf[:n]...)
}

func appendCString(out []byte, s string) []byte {
	out = append(out, s...)
	return append(out, 0)
}

// AppendTo encodes v like juce::var::writeToStream and appends it to out.
func (v Value) AppendTo(out []byte) []byte {
	switch v.Kind {
	case KindVoid:
		return appendCompressedInt(out, 0)
	case KindUndefined:
		out = appendCompressedInt(out, 1)
		return append(out, byte(KindUndefined))
	case KindBoolTrue, KindBoolFalse:
		out = appendCompressedInt(out, 1)
		return append(out, byte(v.Kind))
	case KindInt:
		out = appendCompressedInt(out, 5)
		out = append(out, byte(KindInt))
		return binary.LittleEndian.AppendUint32(out, uint32(int32(v.I)))
	case KindInt64:
		out = appendCompressedInt(out, 9)
		out = append(out, byte(KindInt64))
		return binary.LittleEndian.AppendUint64(out, uint64(v.I))
	case KindDouble:
		out = appendCompressedInt(out, 9)
		out = append(out, byte(KindDouble))
		return binary.LittleEndian.AppendUint64(out, math.Float64bits(v.F))
	case KindString:
		out = appendCompressedInt(out, int64(len(v.S))+2)
		out = append(out, byte(KindString))
		return appendCString(out, v.S)
	case KindBinary:
		out = appendCompressedInt(out, int64(len(v.B))+1)
		out = append(out, byte(KindBinary))
		return append(out, v.B...)
	case KindArray:
		body := appendCompressedInt(nil, int64(len(v.A)))
		for _, e := range v.A {
			body = e.AppendTo(body)
		}
		out = appendCompressedInt(out, int64(len(body))+1)
		out = append(out, byte(KindArray))
		return append(out, body...)
	}
	out = appendCompressedInt(out, int64(len(v.B))+1)
	out = append(out, v.Marker)
	return append(out, v.B...)
}

// Trigger payloads: the momentary "button" pattern the device uses for
// request properties such as mixLinkRequest (two serialised bools).
var (
	TriggerPress   = []byte{0x01, 0x01, 0x02, 0x01, 0x01, 0x02}
	TriggerRelease = []byte{0x01, 0x01, 0x03, 0x01, 0x01, 0x03}
)

// IsTriggerPayload reports whether b looks like a two-bool trigger blob.
func IsTriggerPayload(b []byte) bool {
	return len(b) == 6 && b[0] == 1 && b[1] == 1 && b[3] == 1 && b[4] == 1 &&
		(b[2] == 2 || b[2] == 3) && (b[5] == 2 || b[5] == 3)
}
