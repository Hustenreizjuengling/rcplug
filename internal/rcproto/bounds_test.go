package rcproto

import "testing"

// Hostile counts and nesting stay cheap: an array of millions of voids,
// or arrays nested a thousand deep, are refused without decoding them.
func TestDecoderBoundsHostileInput(t *testing.T) {
	// var: <size><marker><payload>; an array's payload is <count><elements>,
	// and a void element is a single 0 (size 0).
	array := func(payload []byte) []byte {
		out := appendCompressedInt(nil, int64(len(payload)+1))
		out = append(out, byte(KindArray))
		return append(out, payload...)
	}
	n := 1 << 20
	voids := append(appendCompressedInt(nil, int64(n)), make([]byte, n)...)
	if _, err := (&reader{b: array(voids)}).value(); err == nil {
		t.Fatal("an array of a million voids was decoded")
	}
	small := append(appendCompressedInt(nil, 1000), make([]byte, 1000)...)
	if v, err := (&reader{b: array(small)}).value(); err != nil || len(v.A) != 1000 {
		t.Fatalf("a normal array: %v", err)
	}
	nested := array(appendCompressedInt(nil, 0)) // an empty array, wrapped a thousand times
	for i := 0; i < 1000; i++ {
		nested = array(append(appendCompressedInt(nil, 1), nested...))
	}
	if _, err := (&reader{b: nested}).value(); err == nil {
		t.Fatal("arrays nested a thousand deep were decoded")
	}
}
