package console

import (
	"testing"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// A level sent as a double is held to the node's bounds like an integer.
func TestBoundsHoldForDoubles(t *testing.T) {
	for _, kind := range []string{"int", "double"} {
		num := func(x float64) rcproto.Value {
			if kind == "int" {
				return rcproto.Int(int64(x))
			}
			return rcproto.Double(x)
		}
		pot := &rcproto.Node{Type: "POT", Props: []rcproto.Property{
			{Name: "potLevel", Value: num(10)}, {Name: "potMin", Value: num(0)}, {Name: "potMax", Value: num(100)}}}
		for x, ok := range map[float64]bool{50: true, 100: true, 101: false, 150: false, -1: false} {
			v := num(x)
			if err := intBounds(pot, &v, "potMin", "potMax"); (err == nil) != ok {
				t.Errorf("%s %v: %v", kind, x, err)
			}
		}
	}
}
