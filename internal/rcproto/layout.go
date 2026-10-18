package rcproto

import (
	"encoding/json"
)

// Run is a contiguous sequence of same-typed sibling nodes: the i-th node of
// the run lives at Parent + [First+i].
type Run struct {
	Parent []int `json:"parent"`
	First  int   `json:"first"`
	Count  int   `json:"count"`
}

// MarshalJSON always emits parent as an array (never null) for clients.
func (r Run) MarshalJSON() ([]byte, error) {
	type plain Run
	if r.Parent == nil {
		r.Parent = []int{}
	}
	return json.Marshal(plain(r))
}

// Path returns the path of the i-th node of the run, or nil if out of range.
func (r Run) Path(i int) []int {
	if i < 0 || i >= r.Count {
		return nil
	}
	p := make([]int, len(r.Parent)+1)
	copy(p, r.Parent)
	p[len(r.Parent)] = r.First + i
	return p
}

// IndexOf returns the ordinal of path within the run, or -1.
func (r Run) IndexOf(path []int) int {
	if len(path) != len(r.Parent)+1 {
		return -1
	}
	for i := range r.Parent {
		if path[i] != r.Parent[i] {
			return -1
		}
	}
	n := path[len(path)-1] - r.First
	if n < 0 || n >= r.Count {
		return -1
	}
	return n
}

// MixPerSource is the width of the source-major MIX matrix (13 output buses).
const MixPerSource = 13

// Layout describes where the node families live in a full-sync tree. Every
// position is discovered from the tree at runtime, because ordinals shift
// between models and firmware versions; facts the tree does not carry (how
// many faders are physical, pads per bank) come from the model's Profile.
type Layout struct {
	Model     Model  `json:"model"`
	ModelName string `json:"modelName"`
	Firmware  string `json:"firmware"`
	// Controllable mirrors Profile.Controllable: whether writes are offered
	// for this model without an explicit opt-in.
	Controllable bool `json:"controllable"`

	// Runs maps a node type to its first (shallowest) run. Nested runs are
	// also available under "PARENT/TYPE" (e.g. "PADEFFECTS/EFFECTS_PARAMETERS").
	Runs map[string]Run `json:"runs"`

	SourceCount    int `json:"sourceCount"`    // MIX run / 13
	MixPerSource   int `json:"mixPerSource"`   // 13
	PhysicalFaders int `json:"physicalFaders"` // hardware sliders
	VirtualFaders  int `json:"virtualFaders"`  // on-screen strips
	PadsPerBank    int `json:"padsPerBank"`
	PadBanks       int `json:"padBanks"`
	// StripMeters / OutputMeters: METER ordinal ranges when the meter count
	// matches strips+outputs (9+13 on the Duo). Zero counts when unknown.
	StripMeters  Run `json:"stripMeters"`
	OutputMeters Run `json:"outputMeters"`
}

// DiscoverLayout walks the root (depth 1) and the children of each root node
// (depth 2) and records the first run of every node type.
func DiscoverLayout(root *Node) *Layout {
	l := &Layout{Runs: map[string]Run{}, MixPerSource: MixPerSource, Model: ModelUnknown}
	if root == nil {
		return l
	}
	record := func(parent []int, kids []*Node, prefix string) {
		for i := 0; i < len(kids); {
			t := kids[i].Type
			j := i + 1
			for j < len(kids) && kids[j].Type == t {
				j++
			}
			run := Run{Parent: append([]int(nil), parent...), First: i, Count: j - i}
			if prefix != "" {
				if _, ok := l.Runs[prefix+"/"+t]; !ok {
					l.Runs[prefix+"/"+t] = run
				}
			}
			if _, ok := l.Runs[t]; !ok {
				l.Runs[t] = run
			}
			i = j
		}
	}
	record(nil, root.Children, "")
	for i, c := range root.Children {
		if len(c.Children) > 0 {
			record([]int{i}, c.Children, c.Type)
		}
	}

	l.Model = DetectModel(root)
	if sys := l.Node(root, "SYSTEM", 0); sys != nil {
		if fw, ok := sys.Get("systemFirmwareVersion"); ok {
			l.Firmware = fw.S
		}
	}
	p := ProfileOf(l.Model)
	l.ModelName, l.Controllable = p.Name, p.Controllable
	l.PhysicalFaders, l.PadsPerBank, l.PadBanks = p.PhysicalFaders, p.PadsPerBank, p.PadBanks
	faders := l.Runs["FADER"].Count
	if l.PhysicalFaders > faders {
		l.PhysicalFaders = faders
	}
	l.VirtualFaders = faders - l.PhysicalFaders
	if mix := l.Runs["MIX"].Count; mix > 0 && mix%MixPerSource == 0 {
		l.SourceCount = mix / MixPerSource
	}
	if m, ok := l.Runs["METER"]; ok && faders > 0 && m.Count == faders+MixPerSource {
		l.StripMeters = Run{Parent: m.Parent, First: m.First, Count: faders}
		l.OutputMeters = Run{Parent: m.Parent, First: m.First + faders, Count: MixPerSource}
	}
	return l
}

// Node returns the i-th node of the named run, or nil.
func (l *Layout) Node(root *Node, family string, i int) *Node {
	r, ok := l.Runs[family]
	if !ok {
		return nil
	}
	p := r.Path(i)
	if p == nil {
		return nil
	}
	return root.At(p)
}

// Path returns the path of the i-th node of the named run, or nil.
func (l *Layout) Path(family string, i int) []int {
	r, ok := l.Runs[family]
	if !ok {
		return nil
	}
	return r.Path(i)
}

// MixPath returns the path of the routing cell (source → output), or nil.
func (l *Layout) MixPath(source, output int) []int {
	if source < 0 || source >= l.SourceCount || output < 0 || output >= MixPerSource {
		return nil
	}
	return l.Path("MIX", source*MixPerSource+output)
}
