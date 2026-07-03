package config

import (
	"strings"
	"testing"
)

func mustSnapshot(t *testing.T, raw string) *TasksSnapshot {
	t.Helper()
	s, err := ParseTasksSnapshot([]byte(raw))
	if err != nil {
		t.Fatalf("ParseTasksSnapshot: %v", err)
	}
	return s
}

func TestParseTasksSnapshot_Counts(t *testing.T) {
	raw := `[
		{"id":"a-1","issue_type":"bug","status":"open","priority":1,"labels":["x"],"title":"t","assignee":"u","updated_at":"2026-06-30T10:00:00Z"},
		{"id":"a-2","issue_type":"bug","status":"closed","priority":2,"labels":["y"],"title":"t2","assignee":"u","updated_at":"2026-06-30T10:00:00Z"},
		{"id":"a-3","issue_type":"task","status":"in_progress","priority":1,"labels":["x","y"],"title":"t3","assignee":"u","updated_at":"2026-06-30T10:00:00Z"}
	]`
	s := mustSnapshot(t, raw)

	if got, want := len(s.All), 3; got != want {
		t.Fatalf("len(All)=%d want %d", got, want)
	}
	if s.Open != 2 {
		t.Errorf("Open=%d want 2", s.Open)
	}
	if s.Closed != 1 {
		t.Errorf("Closed=%d want 1", s.Closed)
	}
	if s.InProgress != 1 {
		t.Errorf("InProgress=%d want 1", s.InProgress)
	}
	if s.OpenByType["bug"] != 1 {
		t.Errorf("OpenByType[bug]=%d want 1", s.OpenByType["bug"])
	}
	if s.OpenByType["task"] != 1 {
		t.Errorf("OpenByType[task]=%d want 1", s.OpenByType["task"])
	}
	if s.CountByType["bug"] != 2 {
		t.Errorf("CountByType[bug]=%d want 2", s.CountByType["bug"])
	}
	if s.CountByLabel["x"] != 2 {
		t.Errorf("CountByLabel[x]=%d want 2", s.CountByLabel["x"])
	}
	if s.CountByStatus["closed"] != 1 {
		t.Errorf("CountByStatus[closed]=%d want 1", s.CountByStatus["closed"])
	}

	// Verify canonical key mapping (issue_type → type, assignee passthrough).
	first := s.All[0]
	if first[issueKeyType] != "bug" {
		t.Errorf("type=%v want bug", first[issueKeyType])
	}
	if first[issueKeyAssignee] != "u" {
		t.Errorf("assignee=%v want u", first[issueKeyAssignee])
	}
}

func TestParseTasksSnapshot_OwnerFallback(t *testing.T) {
	// bd actually emits `owner` (not `assignee`) — verify fallback.
	s := mustSnapshot(t, `[{"id":"a-1","issue_type":"bug","status":"open","owner":"bob","updated_at":"T"}]`)
	if s.All[0][issueKeyAssignee] != "bob" {
		t.Errorf("owner fallback failed: assignee=%v", s.All[0][issueKeyAssignee])
	}
}

func TestParseTasksSnapshot_Empty(t *testing.T) {
	for _, raw := range []string{"", "[]", "null"} {
		s, err := ParseTasksSnapshot([]byte(raw))
		if err != nil {
			t.Fatalf("ParseTasksSnapshot(%q): %v", raw, err)
		}
		if len(s.All) != 0 {
			t.Errorf("ParseTasksSnapshot(%q): All=%d want 0", raw, len(s.All))
		}
	}
}

func TestParseTasksSnapshot_InvalidJSON(t *testing.T) {
	_, err := ParseTasksSnapshot([]byte("not json"))
	if err == nil {
		t.Errorf("expected parse error for invalid JSON")
	}
}

func TestDiffTasks_AllTransitions(t *testing.T) {
	prev := mustSnapshot(t, `[
		{"id":"a-1","issue_type":"bug","status":"open","priority":1,"labels":[],"updated_at":"T1"},
		{"id":"a-2","issue_type":"bug","status":"closed","priority":2,"labels":[],"updated_at":"T1"},
		{"id":"a-3","issue_type":"task","status":"open","priority":1,"labels":["foo"],"updated_at":"T1"},
		{"id":"a-removed","issue_type":"task","status":"open","priority":3,"labels":[],"updated_at":"T1"}
	]`)
	curr := mustSnapshot(t, `[
		{"id":"a-1","issue_type":"bug","status":"closed","priority":1,"labels":[],"updated_at":"T2"},
		{"id":"a-2","issue_type":"bug","status":"open","priority":2,"labels":[],"updated_at":"T2"},
		{"id":"a-3","issue_type":"task","status":"open","priority":1,"labels":["foo","bar"],"updated_at":"T2"},
		{"id":"a-new","issue_type":"feature","status":"open","priority":3,"labels":[],"updated_at":"T2"}
	]`)

	d := DiffTasks(prev, curr)

	if len(d.Added) != 1 || d.Added[0][issueKeyID] != "a-new" {
		t.Errorf("Added=%v want [a-new]", idsOf(d.Added))
	}
	if len(d.Removed) != 1 || d.Removed[0][issueKeyID] != "a-removed" {
		t.Errorf("Removed=%v want [a-removed]", idsOf(d.Removed))
	}
	// a-1 (status), a-2 (status), a-3 (labels) all count as updated.
	if len(d.Updated) != 3 {
		t.Errorf("Updated=%v want 3 entries", idsOf(d.Updated))
	}
	if len(d.Closed) != 1 || d.Closed[0][issueKeyID] != "a-1" {
		t.Errorf("Closed=%v want [a-1]", idsOf(d.Closed))
	}
	if len(d.Reopened) != 1 || d.Reopened[0][issueKeyID] != "a-2" {
		t.Errorf("Reopened=%v want [a-2]", idsOf(d.Reopened))
	}
	if len(d.LabelAdded) != 1 || d.LabelAdded[0][issueKeyID] != "a-3" {
		t.Errorf("LabelAdded=%v want [a-3]", idsOf(d.LabelAdded))
	}
	// Touched = Added ∪ Updated → 1 + 3 = 4.
	if len(d.Touched) != 4 {
		t.Errorf("Touched=%v want 4 entries", idsOf(d.Touched))
	}
}

func TestDiffTasks_NewIssueWithLabelsIsLabelAdded(t *testing.T) {
	curr := mustSnapshot(t, `[{"id":"a-1","issue_type":"bug","status":"open","priority":1,"labels":["PR opened"],"updated_at":"T1"}]`)
	d := DiffTasks(nil, curr)
	if len(d.Added) != 1 || len(d.LabelAdded) != 1 {
		t.Errorf("Added=%d LabelAdded=%d want 1,1", len(d.Added), len(d.LabelAdded))
	}
}

func TestDiffTasks_NilSnapshots(t *testing.T) {
	d := DiffTasks(nil, nil)
	if d == nil || len(d.Added) != 0 || len(d.Removed) != 0 || len(d.Updated) != 0 {
		t.Errorf("nil/nil diff should be empty: %+v", d)
	}
}

func TestTasksEvaluator_EmptyExpressionFiresAlways(t *testing.T) {
	ev, err := NewTasksConditionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ev.Evaluate("", &TasksChangeContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Errorf("empty expression should return true")
	}
}

func TestTasksEvaluator_CanonicalExpressions(t *testing.T) {
	prev := mustSnapshot(t, `[
		{"id":"a-1","issue_type":"bug","status":"open","priority":2,"labels":["foo"],"updated_at":"T1"},
		{"id":"a-2","issue_type":"bug","status":"closed","priority":1,"labels":[],"updated_at":"T1"}
	]`)
	curr := mustSnapshot(t, `[
		{"id":"a-1","issue_type":"bug","status":"open","priority":2,"labels":["foo","PR opened"],"updated_at":"T2"},
		{"id":"a-2","issue_type":"bug","status":"open","priority":1,"labels":[],"updated_at":"T2"},
		{"id":"a-3","issue_type":"bug","status":"open","priority":1,"labels":[],"updated_at":"T2"}
	]`)
	delta := DiffTasks(prev, curr)
	ctx := &TasksChangeContext{Tasks: curr, Prev: prev, Changes: delta}

	ev, err := NewTasksConditionEvaluator()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		expr string
		want bool
	}{
		{`Tasks.OpenByType["bug"] > Prev.OpenByType["bug"]`, true},
		{`Changes.Touched.exists(i, "PR opened" in i.labels)`, true},
		{`Changes.Added.exists(i, i.type == "bug" && i.priority <= 1)`, true},
		{`size(Changes.Reopened) > 0 || Tasks.Open > Prev.Open`, true},
		// Negative cases against the same fixtures (sanity check that the
		// evaluator can return false, not just always true).
		{`size(Changes.Removed) > 0`, false},
		{`Changes.Added.exists(i, i.type == "feature")`, false},
	}
	for _, c := range cases {
		got, err := ev.Evaluate(c.expr, ctx)
		if err != nil {
			t.Errorf("Evaluate(%q) error: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Evaluate(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestTasksEvaluator_FailClosed(t *testing.T) {
	ev, err := NewTasksConditionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	// Compile error: unknown identifier.
	got, err := ev.Evaluate(`NoSuch.thing > 0`, &TasksChangeContext{})
	if err == nil {
		t.Errorf("expected compile error for unknown identifier")
	}
	if got {
		t.Errorf("compile error must return false (fail-closed), got %v", got)
	}
	// Non-bool result.
	got, err = ev.Evaluate(`1 + 1`, &TasksChangeContext{})
	if err == nil {
		t.Errorf("expected non-bool error")
	}
	if got {
		t.Errorf("non-bool result must return false (fail-closed), got %v", got)
	}
	// Runtime evaluation error: indexing a missing key on an empty map.
	got, err = ev.Evaluate(`Tasks.OpenByType["bug"] > 0`, &TasksChangeContext{})
	if err == nil {
		t.Errorf("expected runtime error for missing key on empty map")
	}
	if got {
		t.Errorf("runtime error must return false (fail-closed), got %v", got)
	}
}

func TestValidateCondition(t *testing.T) {
	if err := ValidateCondition(""); err != nil {
		t.Errorf("empty condition should be valid: %v", err)
	}
	if err := ValidateCondition(`Tasks.Open > Prev.Open`); err != nil {
		t.Errorf("valid condition rejected: %v", err)
	}
	err := ValidateCondition(`Tasks.Open > `)
	if err == nil {
		t.Errorf("syntactically invalid condition should fail")
	}
	if err != nil && !strings.Contains(err.Error(), "compile") {
		t.Errorf("expected compile-time error, got %v", err)
	}
	// Unknown identifier — should also fail at compile.
	if err := ValidateCondition(`NoSuch.thing > 0`); err == nil {
		t.Errorf("unknown identifier should fail compile")
	}
}

func TestTasksEvaluator_CachesPrograms(t *testing.T) {
	ev, err := NewTasksConditionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	expr := `Tasks.Open > 0`
	if _, err := ev.compile(expr); err != nil {
		t.Fatal(err)
	}
	ev.mu.RLock()
	_, cached := ev.cache[expr]
	ev.mu.RUnlock()
	if !cached {
		t.Errorf("compiled program not cached")
	}
}

// idsOf is a tiny helper that extracts ids from a list of issue maps for
// concise error messages.
func idsOf(issues []map[string]any) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		if id, ok := i[issueKeyID].(string); ok {
			out = append(out, id)
		}
	}
	return out
}
