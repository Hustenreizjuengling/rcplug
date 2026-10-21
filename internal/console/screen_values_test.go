package console

import (
	"testing"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// A binary signal property takes its payload as hex; a trigger keeps
// press/release; garbage is refused.
func TestCoerceBinaryHex(t *testing.T) {
	cur := rcproto.Binary([]byte{1, 5, 1, 0, 0, 0, 0, 1, 1, 3})
	v, err := coerce(cur, "padRecordStateRequest", "010501020000000101010")
	if err == nil {
		t.Fatalf("odd-length hex accepted: %v", v)
	}
	v, err = coerce(cur, "padRecordStateRequest", "0105010200000001010102")
	if err != nil || v.Kind != rcproto.KindBinary || len(v.B) != 11 || v.B[3] != 2 {
		t.Fatalf("hex payload: %v %v", v, err)
	}
	if _, err := coerce(cur, "padRecordStateRequest", "zz"); err == nil {
		t.Fatal("non-hex accepted")
	}
	if _, err := coerce(cur, "padRecordStateRequest", 5.0); err == nil {
		t.Fatal("a number for a binary property accepted")
	}
	if v, err := coerce(rcproto.Binary(rcproto.TriggerRelease), "mixLinkRequest", "press"); err != nil || string(v.B) != string(rcproto.TriggerPress) {
		t.Fatalf("trigger press: %v %v", v, err)
	}
}

// The player's file path is the one path the screen level writes, and only
// as an absolute path without "..".
func TestScreenPlayerPath(t *testing.T) {
	if Writable(LevelExpert, "PLAYER", "playerFilePath") {
		t.Fatal("expert level must not write a path")
	}
	if !Writable(LevelScreen, "PLAYER", "playerFilePath") {
		t.Fatal("screen level must write the player's path")
	}
	if Writable(LevelScreen, "PAD", "padFilePath") || Writable(LevelScreen, "SHOW", "showFilePath") {
		t.Fatal("other paths stay denied at screen level")
	}
}

// The dim time arrives as a bool from the factory and becomes an int once
// set, as RØDE's screen writes it.
func TestCoerceDimAfterSeconds(t *testing.T) {
	v, err := coerce(rcproto.Bool(false), "screenDimAfterSeconds", 300.0)
	if err != nil || v.Kind != rcproto.KindInt || v.I != 300 {
		t.Fatalf("seconds for the factory bool: %v %v", v, err)
	}
	if _, err := coerce(rcproto.Bool(false), "screenDimAfterSeconds", 1.5); err == nil {
		t.Fatal("a fraction accepted")
	}
	if _, err := coerce(rcproto.Bool(false), "systemDateTime24h", 1.0); err == nil {
		t.Fatal("a number for another bool accepted")
	}
}
