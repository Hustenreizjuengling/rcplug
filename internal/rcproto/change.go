package rcproto

import (
	"fmt"
)

// ChangeType is the juce::ValueTreeSynchroniser message type (first byte).
type ChangeType uint8

const (
	PropertyChanged ChangeType = 1
	FullSync        ChangeType = 2
	ChildAdded      ChangeType = 3
	ChildRemoved    ChangeType = 4
	ChildMoved      ChangeType = 5
	PropertyRemoved ChangeType = 6
)

func (t ChangeType) String() string {
	switch t {
	case PropertyChanged:
		return "propertyChanged"
	case FullSync:
		return "fullSync"
	case ChildAdded:
		return "childAdded"
	case ChildRemoved:
		return "childRemoved"
	case ChildMoved:
		return "childMoved"
	case PropertyRemoved:
		return "propertyRemoved"
	}
	return fmt.Sprintf("change(%d)", uint8(t))
}

// Change is one decoded synchroniser message.
//
// Path addresses a node by child indices from the root (root-first). For
// property changes it is the node carrying the property; for child changes it
// is the parent.
type Change struct {
	Type     ChangeType
	Path     []int
	Name     string // property changes
	Value    Value  // PropertyChanged
	Index    int    // ChildAdded insertion index, ChildRemoved/Moved old index
	NewIndex int    // ChildMoved
	Tree     *Node  // FullSync root, ChildAdded subtree
}

// Structural reports whether the change alters tree topology (and therefore
// invalidates index paths held elsewhere).
func (c *Change) Structural() bool {
	switch c.Type {
	case FullSync, ChildAdded, ChildRemoved, ChildMoved:
		return true
	}
	return false
}

func (r *reader) path() ([]int, error) {
	n, err := r.count()
	if err != nil {
		return nil, err
	}
	p := make([]int, n)
	for i := range p {
		v, err := r.compressedInt()
		if err != nil {
			return nil, err
		}
		if v < 0 || v > 1<<24 {
			return nil, fmt.Errorf("rcproto: bad path index %d", v)
		}
		p[i] = int(v)
	}
	return p, nil
}

func (r *reader) index() (int, error) {
	v, err := r.compressedInt()
	if err != nil {
		return 0, err
	}
	if v < 0 || v > 1<<24 {
		return 0, fmt.Errorf("rcproto: bad child index %d", v)
	}
	return int(v), nil
}

// DecodeChange decodes exactly one change-frame payload.
func DecodeChange(payload []byte) (*Change, error) {
	r := &reader{b: payload}
	t, err := r.u8()
	if err != nil {
		return nil, err
	}
	c := &Change{Type: ChangeType(t)}
	switch c.Type {
	case PropertyChanged:
		if c.Path, err = r.path(); err != nil {
			return nil, err
		}
		if c.Name, err = r.cstring(); err != nil {
			return nil, err
		}
		if c.Value, err = r.value(); err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name, err)
		}
	case PropertyRemoved:
		if c.Path, err = r.path(); err != nil {
			return nil, err
		}
		if c.Name, err = r.cstring(); err != nil {
			return nil, err
		}
	case FullSync:
		if c.Tree, err = r.node(0); err != nil {
			return nil, err
		}
	case ChildAdded:
		if c.Path, err = r.path(); err != nil {
			return nil, err
		}
		if c.Index, err = r.index(); err != nil {
			return nil, err
		}
		if c.Tree, err = r.node(0); err != nil {
			return nil, err
		}
	case ChildRemoved:
		if c.Path, err = r.path(); err != nil {
			return nil, err
		}
		if c.Index, err = r.index(); err != nil {
			return nil, err
		}
	case ChildMoved:
		if c.Path, err = r.path(); err != nil {
			return nil, err
		}
		if c.Index, err = r.index(); err != nil {
			return nil, err
		}
		if c.NewIndex, err = r.index(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("rcproto: unknown change type %d", t)
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("rcproto: %d trailing bytes after %s", r.remaining(), c.Type)
	}
	return c, nil
}

func appendPath(out []byte, path []int) []byte {
	out = appendCompressedInt(out, int64(len(path)))
	for _, p := range path {
		out = appendCompressedInt(out, int64(p))
	}
	return out
}

// Encode serialises the change as a synchroniser payload.
func (c *Change) Encode() []byte {
	out := []byte{byte(c.Type)}
	switch c.Type {
	case PropertyChanged:
		out = appendPath(out, c.Path)
		out = appendCString(out, c.Name)
		out = c.Value.AppendTo(out)
	case PropertyRemoved:
		out = appendPath(out, c.Path)
		out = appendCString(out, c.Name)
	case FullSync:
		out = c.Tree.AppendTo(out)
	case ChildAdded:
		out = appendPath(out, c.Path)
		out = appendCompressedInt(out, int64(c.Index))
		out = c.Tree.AppendTo(out)
	case ChildRemoved:
		out = appendPath(out, c.Path)
		out = appendCompressedInt(out, int64(c.Index))
	case ChildMoved:
		out = appendPath(out, c.Path)
		out = appendCompressedInt(out, int64(c.Index))
		out = appendCompressedInt(out, int64(c.NewIndex))
	}
	return out
}

// EncodePropertyChanged is a shortcut for the only message clients normally send.
func EncodePropertyChanged(path []int, name string, v Value) []byte {
	return (&Change{Type: PropertyChanged, Path: path, Name: name, Value: v}).Encode()
}

// Apply applies a change to root the way the receiving
// ValueTreeSynchroniser::applyChange does. For FullSync it returns the new
// root; otherwise it returns root. Changes addressing missing nodes are
// rejected with an error and leave the tree untouched.
func Apply(root *Node, c *Change) (*Node, error) {
	if c.Type == FullSync {
		if c.Tree == nil {
			return root, fmt.Errorf("rcproto: fullSync without tree")
		}
		return c.Tree, nil
	}
	if root == nil {
		return nil, fmt.Errorf("rcproto: %s before full sync", c.Type)
	}
	n := root.At(c.Path)
	if n == nil {
		return root, fmt.Errorf("rcproto: %s: no node at path %v", c.Type, c.Path)
	}
	switch c.Type {
	case PropertyChanged:
		n.Set(c.Name, c.Value)
	case PropertyRemoved:
		n.Remove(c.Name)
	case ChildAdded:
		if c.Tree == nil {
			return root, fmt.Errorf("rcproto: childAdded without subtree")
		}
		idx := c.Index
		if idx < 0 || idx > len(n.Children) {
			idx = len(n.Children) // JUCE appends on an out-of-range index
		}
		n.Children = append(n.Children, nil)
		copy(n.Children[idx+1:], n.Children[idx:])
		n.Children[idx] = c.Tree
	case ChildRemoved:
		if c.Index < 0 || c.Index >= len(n.Children) {
			return root, fmt.Errorf("rcproto: childRemoved: index %d out of range", c.Index)
		}
		n.Children = append(n.Children[:c.Index], n.Children[c.Index+1:]...)
	case ChildMoved:
		if c.Index < 0 || c.Index >= len(n.Children) || c.NewIndex < 0 || c.NewIndex >= len(n.Children) {
			return root, fmt.Errorf("rcproto: childMoved: index out of range")
		}
		ch := n.Children[c.Index]
		n.Children = append(n.Children[:c.Index], n.Children[c.Index+1:]...)
		n.Children = append(n.Children, nil)
		copy(n.Children[c.NewIndex+1:], n.Children[c.NewIndex:])
		n.Children[c.NewIndex] = ch
	default:
		return root, fmt.Errorf("rcproto: cannot apply %s", c.Type)
	}
	return root, nil
}
