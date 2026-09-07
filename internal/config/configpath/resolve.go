package configpath

import "fmt"

// Resolve merges assignments — possibly gathered from multiple flags and
// multiple modes, in the exact order the caller applies them — into an
// ordered, deduplicated OpSet. Assignments targeting the exact same path
// are last-wins, regardless of which mode produced them (mixed-mode
// precedence follows argument order only). Assignments whose paths are in
// an ancestor/descendant relationship (e.g. `a.b` and `a`, or a scalar and
// an object at the same path) are rejected as ambiguous.
func Resolve(assignments []Assignment) (OpSet, error) {
	if len(assignments) > MaxOperations {
		return OpSet{}, limitErr("", "too many operations")
	}

	byPath := make(map[string]int, len(assignments))
	var ops []Op
	for _, a := range assignments {
		key := a.Path.String()
		if idx, ok := byPath[key]; ok {
			ops[idx].Value = a.Value
			continue
		}
		byPath[key] = len(ops)
		ops = append(ops, Op{Path: a.Path, Value: a.Value})
	}

	if err := checkConflicts(ops); err != nil {
		return OpSet{}, err
	}
	return OpSet{Ops: ops}, nil
}

func checkConflicts(ops []Op) error {
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			if isAncestor(ops[i].Path, ops[j].Path) || isAncestor(ops[j].Path, ops[i].Path) {
				return conflictErr("", fmt.Sprintf("conflicting paths %q and %q", ops[i].Path.String(), ops[j].Path.String()))
			}
		}
	}
	return nil
}

func isAncestor(a, b Path) bool {
	if len(a) >= len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
