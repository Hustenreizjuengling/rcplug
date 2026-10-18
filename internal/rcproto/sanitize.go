package rcproto

import "strings"

// Secret reports whether a property holds a credential or personal datum
// (passwords, pairing codes, call details, …). The console never serves
// these values and never accepts writes to them.
func Secret(nodeType, prop string) bool {
	p := strings.ToLower(prop)
	for _, s := range []string{"password", "passwd", "psk", "secret", "token", "usercode", "paircode",
		"rodecode", "username", "calldetails", "calladdress", "btpairednumber",
		// account and server details of calls, streaming and proxies
		"registrationdetails", "sipaccount", "traversalserver", "icecastaddress", "icecastmountpoint",
		"proxyaddress",
		// what the device sees around it: other people's networks and phones
		"wifiscan", "btscan"} {
		if strings.Contains(p, s) {
			return true
		}
	}
	return nodeType == "OAUTH" && strings.HasPrefix(p, "verificationurl")
}

// Redacted replaces string values in sanitized trees.
const Redacted = "<redacted>"

// keepStrings are string properties that carry structure, not identity, and
// survive sanitizing (the layout and model detection rely on them).
var keepStrings = map[string]bool{
	"systemFirmwareVersion": true,
	"mixLevelWithAnchor":    true,
}

// Sanitize returns a copy of the tree that is safe to publish as a test
// fixture. Node types, property names, order and wire types are kept; every
// string except keepStrings becomes Redacted and every binary blob is zeroed
// (same length). Numbers and booleans are kept because the layout, the model
// detection and the simulator depend on them; none of the known properties
// stores identity in a number. Review new captures before publishing them.
//
// Sanitize is idempotent, so a committed fixture is its own sanitized form.
func Sanitize(root *Node) *Node {
	out := root.Clone()
	sanitizeNode(out)
	return out
}

func sanitizeNode(n *Node) {
	for i := range n.Props {
		p := &n.Props[i]
		p.Value = sanitizeValue(p.Name, p.Value)
	}
	for _, c := range n.Children {
		sanitizeNode(c)
	}
}

func sanitizeValue(name string, v Value) Value {
	switch v.Kind {
	case KindString:
		if !keepStrings[name] {
			v.S = Redacted
		}
	case KindBinary, KindUnknown:
		v.B = make([]byte, len(v.B))
	case KindArray:
		a := make([]Value, len(v.A))
		for i, e := range v.A {
			a[i] = sanitizeValue(name, e)
		}
		v.A = a
	}
	return v
}
