package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Child nodes. RØDE's screen adds and removes a few kinds of nodes in the
// desk's tree: a processing preset (FXPRESET under FXPRESETS) and a CallMe
// quick dial (SIPPHONEBOOK under SIPPHONEBOOKARRAY). The plugin that stands
// in for that screen does the same through api/node/add and api/node/remove;
// nobody else does, and no other kinds of nodes (shows and recordings are
// managed through the desk's own request properties).

// childKinds maps a parent node type to the one child type that may be added
// under it (and removed from it).
var childKinds = map[string]string{
	"FXPRESETS":         "FXPRESET",
	"SIPPHONEBOOKARRAY": "SIPPHONEBOOK",
}

type nodeReq struct {
	Epoch uint64                 `json:"epoch"`
	Path  []int                  `json:"path"`
	Type  string                 `json:"type,omitempty"`
	Props map[string]interface{} `json:"props,omitempty"`
	Index *int                   `json:"index,omitempty"`
}

func decodeNode(w http.ResponseWriter, r *http.Request) (*nodeReq, bool) {
	var req nodeReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		sdk.WriteError(w, errBadRequest("invalid request body: "+err.Error()))
		return nil, false
	}
	for k, v := range req.Props {
		if n, ok := v.(json.Number); ok {
			f, err := n.Float64()
			if err != nil {
				sdk.WriteError(w, errBadRequest("invalid number for "+k))
				return nil, false
			}
			req.Props[k] = f
		}
	}
	return &req, true
}

var errNodesScreenOnly = sdk.NewError(http.StatusForbidden, "screen_required",
	"only the plugin with the screen adds or removes nodes of the desk's tree")

// structural runs a child-node change for the screen plugin: the same gates
// as a property write (permission, write protection, untested model), then
// the change itself under the mirror lock.
func (c *Console) structural(by who, epoch uint64, what string, build func(s desk.Snapshot) (*rcproto.Change, error)) (*rcproto.Change, error) {
	_, limit := c.Level()
	var ch *rcproto.Change
	var err error
	switch {
	case by.plugin == "" || !by.screen():
		err = errNodesScreenOnly
	case !by.can("desk:write"):
		err = errNoDeskWrite(by.plugin)
	case limit == "untested_model":
		err = errUntestedModel
	case limit == "read_only":
		err = desk.ErrReadOnly
	case c.client.Passive():
		err = desk.ErrPassive
	default:
		ch, err = c.client.Write(epoch, build)
	}
	e := AuditEntry{Time: time.Now().Format("15:04:05"), Node: "tree", Path: []int{}, Name: what, Remote: by.label()}
	if ch != nil {
		e.Path = ch.Path
		e.Value = fmt.Sprintf("%s %d", ch.Type, ch.Index)
		if ch.Tree != nil {
			e.Node = ch.Tree.Type
		}
	}
	if err != nil {
		e.Error = err.Error()
		if ae, ok := err.(*sdk.APIError); ok {
			e.Code = ae.Code
		}
		c.opts.Logf("console: %s refused: %v", what, err)
	} else {
		c.opts.Logf("console: %s %s at %v (from %s)", what, e.Node, e.Path, by.label())
	}
	c.appendAudit(e)
	return ch, err
}

// handleNodeAdd is POST api/node/add {"epoch", "path": <parent>, "type",
// "props": {name: value}, "index"?}: a child node appended to the parent
// (or inserted at index). The properties' wire types follow a sibling of the
// same type where one exists, else the JSON types.
func (c *Console) handleNodeAdd(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeNode(w, r)
	if !ok {
		return
	}
	ch, err := c.structural(c.who(r), req.Epoch, "node add", func(s desk.Snapshot) (*rcproto.Change, error) {
		if s.Root == nil {
			return nil, desk.ErrNotSynced
		}
		parent := s.Root.At(req.Path)
		if parent == nil {
			return nil, errf(http.StatusBadRequest, "no_node", "no node at path %v", req.Path)
		}
		kind, ok := childKinds[parent.Type]
		if !ok || req.Type != kind {
			return nil, errf(http.StatusForbidden, "node_kind", "%s takes no %q children through the console", parent.Type, req.Type)
		}
		var sibling *rcproto.Node
		for _, ch := range parent.Children {
			if ch.Type == kind {
				sibling = ch
				break
			}
		}
		node := &rcproto.Node{Type: kind}
		names := make([]string, 0, len(req.Props))
		for name := range req.Props {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			if strings.Contains(strings.ToLower(name), "path") {
				return nil, errf(http.StatusForbidden, "denied", "%s.%s is not writable", kind, name)
			}
			var v rcproto.Value
			if sibling != nil {
				cur, ok := sibling.Get(name)
				if !ok {
					return nil, errf(http.StatusBadRequest, "unknown_property", "%s has no property %q (its siblings do not carry it)", kind, name)
				}
				cv, err := coerce(cur, name, req.Props[name])
				if err != nil {
					return nil, errf(http.StatusBadRequest, "invalid_value", "%s.%s: %v", kind, name, err)
				}
				v = cv
			} else {
				v = jsonValue(req.Props[name])
			}
			node.Props = append(node.Props, rcproto.Property{Name: name, Value: v})
		}
		idx := len(parent.Children)
		if req.Index != nil && *req.Index >= 0 && *req.Index < idx {
			idx = *req.Index
		}
		return &rcproto.Change{Type: rcproto.ChildAdded, Path: append([]int(nil), req.Path...), Index: idx, Tree: node}, nil
	})
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": append(append([]int(nil), ch.Path...), ch.Index)})
}

// handleNodeRemove is POST api/node/remove {"epoch", "path": <the node>}.
func (c *Console) handleNodeRemove(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeNode(w, r)
	if !ok {
		return
	}
	_, err := c.structural(c.who(r), req.Epoch, "node remove", func(s desk.Snapshot) (*rcproto.Change, error) {
		if s.Root == nil {
			return nil, desk.ErrNotSynced
		}
		if len(req.Path) == 0 {
			return nil, errf(http.StatusBadRequest, "no_node", "the root is not removable")
		}
		parentPath, idx := req.Path[:len(req.Path)-1], req.Path[len(req.Path)-1]
		parent := s.Root.At(parentPath)
		if parent == nil || idx < 0 || idx >= len(parent.Children) {
			return nil, errf(http.StatusBadRequest, "no_node", "no node at path %v", req.Path)
		}
		node := parent.Children[idx]
		if kind, ok := childKinds[parent.Type]; !ok || node.Type != kind {
			return nil, errf(http.StatusForbidden, "node_kind", "%s nodes are not removed through the console", node.Type)
		}
		if len(node.Children) > 0 {
			return nil, errf(http.StatusForbidden, "node_kind", "%s has children of its own", node.Type)
		}
		return &rcproto.Change{Type: rcproto.ChildRemoved, Path: append([]int(nil), parentPath...), Index: idx}, nil
	})
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// jsonValue picks a wire type for a JSON value with no sibling to follow:
// bool, whole numbers as int, other numbers as double, text as string.
func jsonValue(in interface{}) rcproto.Value {
	switch v := in.(type) {
	case bool:
		return rcproto.Bool(v)
	case float64:
		if v == float64(int64(v)) {
			return rcproto.Int(int64(v))
		}
		return rcproto.Double(v)
	case string:
		return rcproto.String(v)
	}
	return rcproto.String(fmt.Sprint(in))
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
