package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
)

// statusClosed is the canonical status string for closed beads issues.
const statusClosed = "closed"

// Canonical keys exposed to CEL on each issue map. The beads CLI itself uses
// `issue_type` (and `owner`); we normalize so CEL sees `type` (and `assignee`).
const (
	issueKeyID        = "id"
	issueKeyType      = "type"
	issueKeyStatus    = "status"
	issueKeyPriority  = "priority"
	issueKeyLabels    = "labels"
	issueKeyTitle     = "title"
	issueKeyAssignee  = "assignee"
	issueKeyUpdatedAt = "updated_at"
)

// TasksSnapshot is a parsed-and-indexed view of the workspace's beads issues
// at a single point in time. Built from the JSON rows returned by
// `bd list --json --all -n 0` (see internal/beads.Client.List).
type TasksSnapshot struct {
	Open       int
	Closed     int
	InProgress int
	Ready      int
	Blocked    int

	CountByType   map[string]int
	CountByStatus map[string]int
	CountByLabel  map[string]int
	OpenByType    map[string]int

	// All is the list of issues as plain maps with canonical keys (id, type,
	// status, priority, labels, title, assignee, updated_at). Suitable for
	// direct exposure to CEL via the activation.
	All []map[string]any

	// byID indexes All by issue id for fast diffing. Unexported — internal
	// to this package; diffs are computed via DiffTasks.
	byID map[string]map[string]any
}

// TasksDelta captures the difference between a previous and a current
// TasksSnapshot, keyed by issue id. All slices are non-nil (possibly empty)
// so CEL exists/size operations always behave.
type TasksDelta struct {
	Added      []map[string]any
	Updated    []map[string]any
	Removed    []map[string]any
	Closed     []map[string]any
	Reopened   []map[string]any
	LabelAdded []map[string]any
	Touched    []map[string]any // = Added ∪ Updated
}

// TasksChangeContext is the activation payload passed to
// TasksConditionEvaluator.Evaluate. Any nil field is treated as an empty
// snapshot / empty delta — never causes a panic.
type TasksChangeContext struct {
	Tasks   *TasksSnapshot
	Prev    *TasksSnapshot
	Changes *TasksDelta
}

// ParseTasksSnapshot parses the raw JSON bytes (the output of
// `bd list --json --all -n 0`) and returns a TasksSnapshot with derived
// counts and per-id index. Empty or `null` input yields an empty snapshot
// (no error). Rows missing an id are skipped.
func ParseTasksSnapshot(raw []byte) (*TasksSnapshot, error) {
	snap := newEmptySnapshot()
	if len(raw) == 0 || string(raw) == "null" {
		return snap, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("tasks: failed to parse beads list JSON: %w", err)
	}
	for _, r := range rows {
		issue := canonicalizeIssue(r)
		id, _ := issue[issueKeyID].(string)
		if id == "" {
			continue
		}
		snap.All = append(snap.All, issue)
		snap.byID[id] = issue

		status, _ := issue[issueKeyStatus].(string)
		typ, _ := issue[issueKeyType].(string)
		switch status {
		case statusClosed:
			snap.Closed++
		case "in_progress":
			snap.InProgress++
			snap.Open++
		case "ready":
			snap.Ready++
			snap.Open++
		case "blocked":
			snap.Blocked++
			snap.Open++
		case "open":
			snap.Open++
		default:
			if status != "" {
				snap.Open++
			}
		}
		if typ != "" {
			snap.CountByType[typ]++
			if status != statusClosed {
				snap.OpenByType[typ]++
			}
		}
		if status != "" {
			snap.CountByStatus[status]++
		}
		if labels, ok := issue[issueKeyLabels].([]string); ok {
			for _, l := range labels {
				snap.CountByLabel[l]++
			}
		}
	}
	return snap, nil
}

// newEmptySnapshot returns a TasksSnapshot with all maps/slices initialized.
func newEmptySnapshot() *TasksSnapshot {
	return &TasksSnapshot{
		CountByType:   map[string]int{},
		CountByStatus: map[string]int{},
		CountByLabel:  map[string]int{},
		OpenByType:    map[string]int{},
		All:           []map[string]any{},
		byID:          map[string]map[string]any{},
	}
}

// canonicalizeIssue normalizes a raw beads issue row to the canonical key set
// exposed to CEL. The beads CLI uses `issue_type` (not `type`); the spec also
// uses `assignee` while bd uses `owner` — we accept either, preferring the
// spec name when both are present.
func canonicalizeIssue(row map[string]any) map[string]any {
	out := map[string]any{}
	out[issueKeyID] = stringField(row, "id")
	if t := stringField(row, "type"); t != "" {
		out[issueKeyType] = t
	} else {
		out[issueKeyType] = stringField(row, "issue_type")
	}
	out[issueKeyStatus] = stringField(row, "status")
	out[issueKeyPriority] = intField(row, "priority")
	out[issueKeyLabels] = stringSliceField(row, "labels")
	out[issueKeyTitle] = stringField(row, "title")
	if a := stringField(row, "assignee"); a != "" {
		out[issueKeyAssignee] = a
	} else {
		out[issueKeyAssignee] = stringField(row, "owner")
	}
	out[issueKeyUpdatedAt] = stringField(row, "updated_at")
	return out
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intField(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case float64:
			return int64(x)
		case int:
			return int64(x)
		case int64:
			return x
		case json.Number:
			n, _ := x.Int64()
			return n
		}
	}
	return 0
}

func stringSliceField(m map[string]any, key string) []string {
	out := []string{}
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case []string:
			return append(out, x...)
		case []any:
			for _, item := range x {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// DiffTasks computes a TasksDelta from a previous and current snapshot. Nil
// inputs are treated as empty snapshots — a nil prev means every current
// issue is Added; a nil curr means every previous issue is Removed.
func DiffTasks(prev, curr *TasksSnapshot) *TasksDelta {
	delta := &TasksDelta{
		Added:      []map[string]any{},
		Updated:    []map[string]any{},
		Removed:    []map[string]any{},
		Closed:     []map[string]any{},
		Reopened:   []map[string]any{},
		LabelAdded: []map[string]any{},
		Touched:    []map[string]any{},
	}
	if curr == nil {
		curr = newEmptySnapshot()
	}
	if prev == nil {
		prev = newEmptySnapshot()
	}
	for id, currIssue := range curr.byID {
		prevIssue, existed := prev.byID[id]
		if !existed {
			delta.Added = append(delta.Added, currIssue)
			delta.Touched = append(delta.Touched, currIssue)
			if len(stringSliceFromIssue(currIssue, issueKeyLabels)) > 0 {
				delta.LabelAdded = append(delta.LabelAdded, currIssue)
			}
			continue
		}
		changed := false
		if stringFromIssue(currIssue, issueKeyUpdatedAt) != stringFromIssue(prevIssue, issueKeyUpdatedAt) {
			changed = true
		}
		currStatus := stringFromIssue(currIssue, issueKeyStatus)
		prevStatus := stringFromIssue(prevIssue, issueKeyStatus)
		if currStatus != prevStatus {
			changed = true
			if currStatus == statusClosed {
				delta.Closed = append(delta.Closed, currIssue)
			}
			if prevStatus == statusClosed && currStatus != statusClosed {
				delta.Reopened = append(delta.Reopened, currIssue)
			}
		}
		currLabels := stringSliceFromIssue(currIssue, issueKeyLabels)
		prevLabels := stringSliceFromIssue(prevIssue, issueKeyLabels)
		if !labelsEqual(currLabels, prevLabels) {
			changed = true
			if labelsGained(currLabels, prevLabels) {
				delta.LabelAdded = append(delta.LabelAdded, currIssue)
			}
		}
		if changed {
			delta.Updated = append(delta.Updated, currIssue)
			delta.Touched = append(delta.Touched, currIssue)
		}
	}
	for id, prevIssue := range prev.byID {
		if _, stillThere := curr.byID[id]; !stillThere {
			delta.Removed = append(delta.Removed, prevIssue)
		}
	}
	return delta
}

func stringFromIssue(issue map[string]any, key string) string {
	if v, ok := issue[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringSliceFromIssue(issue map[string]any, key string) []string {
	if v, ok := issue[key]; ok {
		if s, ok := v.([]string); ok {
			return s
		}
	}
	return nil
}

func labelsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// labelsGained reports whether curr contains at least one label not present in prev.
func labelsGained(curr, prev []string) bool {
	prevSet := map[string]struct{}{}
	for _, l := range prev {
		prevSet[l] = struct{}{}
	}
	for _, l := range curr {
		if _, ok := prevSet[l]; !ok {
			return true
		}
	}
	return false
}

// TasksConditionEvaluator compiles and evaluates CEL conditions against a
// TasksChangeContext. Compiled programs are cached by expression string for
// reuse across evaluations — same pattern as CELEvaluator.
type TasksConditionEvaluator struct {
	env   *cel.Env
	mu    sync.RWMutex
	cache map[string]cel.Program
}

// NewTasksConditionEvaluator creates a TasksConditionEvaluator with the
// `Tasks`, `Prev`, and `Changes` variables declared as map<string,dyn> so
// that field access, map subscript, exists, and `in` all type-check.
func NewTasksConditionEvaluator() (*TasksConditionEvaluator, error) {
	mapType := cel.MapType(cel.StringType, cel.DynType)
	env, err := cel.NewEnv(
		cel.Variable("Tasks", mapType),
		cel.Variable("Prev", mapType),
		cel.Variable("Changes", mapType),
	)
	if err != nil {
		return nil, fmt.Errorf("tasks: failed to build CEL env: %w", err)
	}
	return &TasksConditionEvaluator{
		env:   env,
		cache: map[string]cel.Program{},
	}, nil
}

// ValidateCondition compiles expr in a fresh tasks-condition env and returns
// any compile-time error. Empty expressions are always valid (they fire on
// any change). This is the entry point wired into W1's session.ConditionValidator
// seam by the config package.
func ValidateCondition(expr string) error {
	if expr == "" {
		return nil
	}
	ev, err := NewTasksConditionEvaluator()
	if err != nil {
		return err
	}
	_, err = ev.compile(expr)
	return err
}

// compile returns the cached cel.Program for expr, building one on first use.
func (e *TasksConditionEvaluator) compile(expr string) (cel.Program, error) {
	e.mu.RLock()
	if prog, ok := e.cache[expr]; ok {
		e.mu.RUnlock()
		return prog, nil
	}
	e.mu.RUnlock()
	ast, issues := e.env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("tasks: compile error in %q: %w", expr, issues.Err())
	}
	prog, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("tasks: program error in %q: %w", expr, err)
	}
	e.mu.Lock()
	e.cache[expr] = prog
	e.mu.Unlock()
	return prog, nil
}

// Evaluate runs expr against ctx and returns whether the trigger should fire.
// Empty expr returns (true, nil) — the trigger fires on any change.
// Compile errors, evaluation errors, and non-bool results are FAIL-CLOSED:
// the method returns (false, err) so a misconfigured condition does NOT
// silently fire.
func (e *TasksConditionEvaluator) Evaluate(expr string, ctx *TasksChangeContext) (bool, error) {
	if expr == "" {
		return true, nil
	}
	prog, err := e.compile(expr)
	if err != nil {
		return false, err
	}
	out, _, err := prog.Eval(buildTasksActivation(ctx))
	if err != nil {
		return false, fmt.Errorf("tasks: evaluation error for %q: %w", expr, err)
	}
	result, ok := out.(types.Bool)
	if !ok {
		return false, fmt.Errorf("tasks: expression %q did not return a bool (got %T)", expr, out)
	}
	return bool(result), nil
}

// buildTasksActivation converts a TasksChangeContext into the activation map
// passed to cel.Program.Eval. All three top-level keys are always present
// even when the corresponding context field is nil.
func buildTasksActivation(ctx *TasksChangeContext) map[string]any {
	if ctx == nil {
		ctx = &TasksChangeContext{}
	}
	return map[string]any{
		"Tasks":   snapshotToActivation(ctx.Tasks),
		"Prev":    snapshotToActivation(ctx.Prev),
		"Changes": deltaToActivation(ctx.Changes),
	}
}

func snapshotToActivation(s *TasksSnapshot) map[string]any {
	if s == nil {
		s = newEmptySnapshot()
	}
	return map[string]any{
		"Open":          int64(s.Open),
		"Closed":        int64(s.Closed),
		"InProgress":    int64(s.InProgress),
		"Ready":         int64(s.Ready),
		"Blocked":       int64(s.Blocked),
		"CountByType":   intMapToAny(s.CountByType),
		"CountByStatus": intMapToAny(s.CountByStatus),
		"CountByLabel":  intMapToAny(s.CountByLabel),
		"OpenByType":    intMapToAny(s.OpenByType),
		"All":           issuesToAny(s.All),
	}
}

func deltaToActivation(d *TasksDelta) map[string]any {
	if d == nil {
		d = &TasksDelta{}
	}
	return map[string]any{
		"Added":      issuesToAny(d.Added),
		"Updated":    issuesToAny(d.Updated),
		"Removed":    issuesToAny(d.Removed),
		"Closed":     issuesToAny(d.Closed),
		"Reopened":   issuesToAny(d.Reopened),
		"LabelAdded": issuesToAny(d.LabelAdded),
		"Touched":    issuesToAny(d.Touched),
	}
}

func intMapToAny(m map[string]int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = int64(v)
	}
	return out
}

// issuesToAny converts a list of issue maps into the activation form expected
// by CEL. Each issue is a map[string]any; the `labels` slice is widened to
// []any so `"x" in i.labels` and `i.labels.exists(...)` work cleanly through
// CEL's dyn adapter.
func issuesToAny(in []map[string]any) []any {
	out := make([]any, 0, len(in))
	for _, issue := range in {
		copy := make(map[string]any, len(issue))
		for k, v := range issue {
			if k == issueKeyLabels {
				if labels, ok := v.([]string); ok {
					labs := make([]any, 0, len(labels))
					for _, l := range labels {
						labs = append(labs, l)
					}
					copy[k] = labs
					continue
				}
			}
			copy[k] = v
		}
		out = append(out, copy)
	}
	return out
}
