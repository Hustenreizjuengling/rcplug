package rcproto

import (
	"fmt"
)

// Property is one named value on a node. Order is preserved as on the wire.
type Property struct {
	Name  string
	Value Value
}

// Node is a juce::ValueTree node: a type name, ordered properties and children.
type Node struct {
	Type     string
	Props    []Property
	Children []*Node
}

// Get returns a property value and whether it exists.
func (n *Node) Get(name string) (Value, bool) {
	for i := range n.Props {
		if n.Props[i].Name == name {
			return n.Props[i].Value, true
		}
	}
	return Value{}, false
}

// Has reports whether the node carries the property.
func (n *Node) Has(name string) bool {
	_, ok := n.Get(name)
	return ok
}

// Set sets a property, appending it if it does not exist yet (as JUCE does).
// It returns the previous value and whether the property existed.
func (n *Node) Set(name string, v Value) (Value, bool) {
	for i := range n.Props {
		if n.Props[i].Name == name {
			old := n.Props[i].Value
			n.Props[i].Value = v
			return old, true
		}
	}
	n.Props = append(n.Props, Property{Name: name, Value: v})
	return Value{}, false
}

// Remove deletes a property, reporting whether it existed.
func (n *Node) Remove(name string) bool {
	for i := range n.Props {
		if n.Props[i].Name == name {
			n.Props = append(n.Props[:i], n.Props[i+1:]...)
			return true
		}
	}
	return false
}

// At returns the node at path (child indices from n), or nil.
func (n *Node) At(path []int) *Node {
	cur := n
	for _, idx := range path {
		if cur == nil || idx < 0 || idx >= len(cur.Children) {
			return nil
		}
		cur = cur.Children[idx]
	}
	return cur
}

// Clone returns a deep copy.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := &Node{Type: n.Type, Props: make([]Property, len(n.Props)), Children: make([]*Node, len(n.Children))}
	for i, p := range n.Props {
		c.Props[i] = Property{Name: p.Name, Value: p.Value.clone()}
	}
	for i, ch := range n.Children {
		c.Children[i] = ch.Clone()
	}
	return c
}

func (v Value) clone() Value {
	if v.B != nil {
		v.B = append([]byte(nil), v.B...)
	}
	if v.A != nil {
		a := make([]Value, len(v.A))
		for i, e := range v.A {
			a[i] = e.clone()
		}
		v.A = a
	}
	return v
}

// Count returns the number of nodes and properties in the subtree.
func (n *Node) Count() (nodes, props int) {
	nodes, props = 1, len(n.Props)
	for _, c := range n.Children {
		cn, cp := c.Count()
		nodes += cn
		props += cp
	}
	return
}

func (n *Node) String() string {
	return fmt.Sprintf("%s(%d props, %d children)", n.Type, len(n.Props), len(n.Children))
}

// maxDepth bounds recursion on hostile input.
const maxDepth = 64

// parseNode reads juce::ValueTree::writeToStream output: type name, property
// count, properties (name + var), child count, children.
func (r *reader) node(depth int) (*Node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("rcproto: tree too deep")
	}
	typ, err := r.cstring()
	if err != nil {
		return nil, err
	}
	n := &Node{Type: typ}
	np, err := r.count()
	if err != nil {
		return nil, err
	}
	n.Props = make([]Property, 0, capHint(np))
	for i := 0; i < np; i++ {
		name, err := r.cstring()
		if err != nil {
			return nil, err
		}
		v, err := r.value()
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", typ, name, err)
		}
		n.Props = append(n.Props, Property{Name: name, Value: v})
	}
	nc, err := r.count()
	if err != nil {
		return nil, err
	}
	n.Children = make([]*Node, 0, capHint(nc))
	for i := 0; i < nc; i++ {
		c, err := r.node(depth + 1)
		if err != nil {
			return nil, err
		}
		n.Children = append(n.Children, c)
	}
	return n, nil
}

// ParseNode decodes one ValueTree stream and requires it to fill b exactly.
func ParseNode(b []byte) (*Node, error) {
	r := &reader{b: b}
	n, err := r.node(0)
	if err != nil {
		return nil, err
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("rcproto: %d trailing bytes after tree", r.remaining())
	}
	return n, nil
}

// AppendTo encodes the subtree like juce::ValueTree::writeToStream.
func (n *Node) AppendTo(out []byte) []byte {
	out = appendCString(out, n.Type)
	out = appendCompressedInt(out, int64(len(n.Props)))
	for _, p := range n.Props {
		out = appendCString(out, p.Name)
		out = p.Value.AppendTo(out)
	}
	out = appendCompressedInt(out, int64(len(n.Children)))
	for _, c := range n.Children {
		out = c.AppendTo(out)
	}
	return out
}
