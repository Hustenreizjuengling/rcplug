package desk

import (
	"bytes"
	"encoding/hex"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// SecretFunc reports whether a property of a node type holds a secret that
// must be redacted before it leaves the device (see console.Secret).
type SecretFunc func(nodeType, prop string) bool

// RedactedText replaces secret values in JSON output.
const RedactedText = "••••••"

// TypeCode returns the one-letter type tag sent alongside values ('r' for a
// redacted secret). See rcproto.Value.TypeCode for the others.
func TypeCode(v rcproto.Value, secret bool) byte {
	if secret {
		return 'r'
	}
	return v.TypeCode()
}

// WriteValue writes v as JSON. Binary is hex; non-finite doubles are null.
// With compact, doubles use 7 significant digits (plenty for live display,
// and it roughly halves meter-heavy change batches).
func WriteValue(b *bytes.Buffer, v rcproto.Value, secret, compact bool) {
	if secret {
		if v.Kind == rcproto.KindString && v.S == "" {
			b.WriteString(`""`)
		} else {
			writeJSONString(b, RedactedText)
		}
		return
	}
	switch v.Kind {
	case rcproto.KindInt, rcproto.KindInt64:
		b.WriteString(strconv.FormatInt(v.I, 10))
	case rcproto.KindDouble:
		switch {
		case math.IsNaN(v.F) || math.IsInf(v.F, 0):
			b.WriteString("null")
		case compact:
			b.WriteString(strconv.FormatFloat(v.F, 'g', 7, 64))
		default:
			b.WriteString(strconv.FormatFloat(v.F, 'g', -1, 64))
		}
	case rcproto.KindBoolTrue:
		b.WriteString("true")
	case rcproto.KindBoolFalse:
		b.WriteString("false")
	case rcproto.KindString:
		writeJSONString(b, v.S)
	case rcproto.KindBinary, rcproto.KindUnknown:
		b.WriteByte('"')
		b.WriteString(hex.EncodeToString(v.B))
		b.WriteByte('"')
	case rcproto.KindArray:
		b.WriteByte('[')
		for i, e := range v.A {
			if i > 0 {
				b.WriteByte(',')
			}
			WriteValue(b, e, false, compact)
		}
		b.WriteByte(']')
	default:
		b.WriteString("null")
	}
}

// writeJSONString writes s as a JSON string literal (invalid UTF-8 replaced,
// HTML-sensitive characters escaped).
func writeJSONString(b *bytes.Buffer, s string) {
	const hexd = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				b.WriteByte('\\')
				b.WriteByte(c)
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				b.WriteString(`\u00`)
				b.WriteByte(hexd[c>>4])
				b.WriteByte(hexd[c&0xf])
			default:
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteString(`�`)
		case r == ' ' || r == ' ':
			b.WriteString(`\u202`)
			b.WriteByte(hexd[r&0xf])
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// WriteTree writes a node as {"t":type,"p":{name:value},"y":"types","c":[...]}
// where "y" holds one type letter per property, in property order.
func WriteTree(b *bytes.Buffer, n *rcproto.Node, secret SecretFunc) {
	b.WriteString(`{"t":`)
	writeJSONString(b, n.Type)
	b.WriteString(`,"p":{`)
	types := make([]byte, 0, len(n.Props))
	for i, p := range n.Props {
		if i > 0 {
			b.WriteByte(',')
		}
		s := secret != nil && secret(n.Type, p.Name)
		writeJSONString(b, p.Name)
		b.WriteByte(':')
		WriteValue(b, p.Value, s, false)
		types = append(types, TypeCode(p.Value, s))
	}
	b.WriteString(`},"y":"`)
	b.Write(types)
	b.WriteString(`","c":[`)
	for i, c := range n.Children {
		if i > 0 {
			b.WriteByte(',')
		}
		WriteTree(b, c, secret)
	}
	b.WriteString(`]}`)
}
