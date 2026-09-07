package configsvc

import (
	"fmt"

	"github.com/inercia/mitto/internal/config/configpath"
)

// navigate walks doc following path and returns the value found there, or
// ok=false if any intermediate segment is absent or type-mismatched (map
// key missing, index out of range, or indexing into a non-container).
func navigate(doc interface{}, path configpath.Path) (interface{}, bool) {
	cur := doc
	for _, seg := range path {
		if seg.IsIndex {
			arr, ok := cur.([]interface{})
			if !ok || seg.Index < 0 || seg.Index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.Index]
			continue
		}
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, present := m[seg.Key]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// redactTree returns a deep copy of node with any registry-flagged secret
// subtree replaced by RedactedPlaceholder. prefix is the path already
// consumed to reach node (nil/empty when node is the whole document).
// Returns whether ANY redaction occurred anywhere in the returned tree.
func redactTree(node interface{}, prefix configpath.Path, reg *Registry) (interface{}, bool) {
	if len(prefix) > 0 {
		if e, ok := reg.Lookup(prefix); ok && e.Redact {
			// An exact-match redacted leaf, or the root of a redacted
			// dynamic subtree (e.g. "mcp"): stop here, whole value hidden.
			if !e.Dynamic || prefix[len(prefix)-1].Key == e.Path || len(prefix) == 1 {
				return RedactedPlaceholder, true
			}
		}
	}
	switch t := node.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		any := false
		for k, v := range t {
			nv, redacted := redactTree(v, append(append(configpath.Path{}, prefix...), configpath.PathSegment{Key: k}), reg)
			out[k] = nv
			any = any || redacted
		}
		return out, any
	case []interface{}:
		out := make([]interface{}, len(t))
		any := false
		for i, v := range t {
			nv, redacted := redactTree(v, append(append(configpath.Path{}, prefix...), configpath.PathSegment{Index: i, IsIndex: true}), reg)
			out[i] = nv
			any = any || redacted
		}
		return out, any
	default:
		return node, false
	}
}

// setAt returns a copy of root with the value at path replaced by newVal,
// creating intermediate maps/arrays as needed. Arrays may only grow by
// exactly one element at a time (append semantics); a sparse index is
// rejected as a structural error. Indexing into, or replacing a container
// with, an incompatible existing type is also a structural error.
func setAt(root interface{}, path configpath.Path, newVal interface{}) (interface{}, error) {
	if len(path) == 0 {
		return newVal, nil
	}
	seg := path[0]
	rest := path[1:]

	if seg.IsIndex {
		var arr []interface{}
		switch t := root.(type) {
		case nil:
			arr = nil
		case []interface{}:
			arr = append([]interface{}{}, t...)
		default:
			return nil, fmt.Errorf("cannot index into non-array value")
		}
		switch {
		case seg.Index < len(arr):
			child, err := setAt(arr[seg.Index], rest, newVal)
			if err != nil {
				return nil, err
			}
			arr[seg.Index] = child
		case seg.Index == len(arr):
			child, err := setAt(nil, rest, newVal)
			if err != nil {
				return nil, err
			}
			arr = append(arr, child)
		default:
			return nil, fmt.Errorf("array index %d out of range (length %d)", seg.Index, len(arr))
		}
		return arr, nil
	}

	var m map[string]interface{}
	switch t := root.(type) {
	case nil:
		m = map[string]interface{}{}
	case map[string]interface{}:
		m = make(map[string]interface{}, len(t)+1)
		for k, v := range t {
			m[k] = v
		}
	default:
		return nil, fmt.Errorf("cannot set key %q into non-object value", seg.Key)
	}
	child, err := setAt(m[seg.Key], rest, newVal)
	if err != nil {
		return nil, err
	}
	m[seg.Key] = child
	return m, nil
}
