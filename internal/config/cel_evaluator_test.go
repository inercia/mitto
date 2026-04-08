package config

import (
	"testing"
)

func newTestEvaluator(t *testing.T) *CELEvaluator {
	t.Helper()
	e, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	return e
}

func compile(t *testing.T, e *CELEvaluator, expr string) *CompiledExpression {
	t.Helper()
	ce, err := e.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	return ce
}

func evaluate(t *testing.T, e *CELEvaluator, ce *CompiledExpression, ctx *PromptEnabledContext) bool {
	t.Helper()
	result, err := e.Evaluate(ce, ctx)
	if err != nil {
		t.Fatalf("Evaluate(%q): %v", ce.String(), err)
	}
	return result
}

// TestCELEvaluator_ExampleExpressions validates the example expressions from the task spec.
func TestCELEvaluator_ExampleExpressions(t *testing.T) {
	e := newTestEvaluator(t)

	childCtx := &PromptEnabledContext{
		ACP:      ACPContext{Name: "auggie", Type: "auggie", Tags: []string{"coding", "fast"}},
		Session:  SessionContext{ID: "child-1", IsChild: true, ParentID: "parent-1"},
		Parent:   ParentContext{Exists: true, Name: "Parent Session", ACPServer: "auggie"},
		Children: ChildrenContext{Count: 0, Exists: false},
		Tools:    ToolsContext{Available: true, Names: []string{"github_create_pr", "github_list_issues", "slack_post"}},
	}

	rootCtx := &PromptEnabledContext{
		ACP:      ACPContext{Name: "claude-code", Type: "claude", Tags: []string{"thinking"}},
		Session:  SessionContext{ID: "root-1", IsChild: false},
		Children: ChildrenContext{Count: 2, Exists: true, Names: []string{"Child A", "Child B"}},
		Tools:    ToolsContext{Available: true, Names: []string{"jira_create_issue", "confluence_search"}},
	}

	tests := []struct {
		expr string
		ctx  *PromptEnabledContext
		want bool
	}{
		// !session.isChild — hide if this is a child
		{expr: "!session.isChild", ctx: rootCtx, want: true},
		{expr: "!session.isChild", ctx: childCtx, want: false},

		// session.isChild && parent.exists — only show in children
		{expr: "session.isChild && parent.exists", ctx: childCtx, want: true},
		{expr: "session.isChild && parent.exists", ctx: rootCtx, want: false},

		// "coding" in acp.tags — only for coding servers
		{expr: `"coding" in acp.tags`, ctx: childCtx, want: true},
		{expr: `"coding" in acp.tags`, ctx: rootCtx, want: false},

		// children.count > 0 — only if has children
		{expr: "children.count > 0", ctx: rootCtx, want: true},
		{expr: "children.count > 0", ctx: childCtx, want: false},

		// tools.hasPattern("github_*") — only if GitHub tools available
		{expr: `tools.hasPattern("github_*")`, ctx: childCtx, want: true},
		{expr: `tools.hasPattern("github_*")`, ctx: rootCtx, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			got := evaluate(t, e, ce, tt.ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_NilContextDefaultsToTrue ensures nil context returns true.
func TestCELEvaluator_NilContextDefaultsToTrue(t *testing.T) {
	e := newTestEvaluator(t)
	ce := compile(t, e, "session.isChild")
	result, err := e.Evaluate(ce, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("nil context should default to true (visible)")
	}
}

// TestCELEvaluator_CompileError ensures invalid expressions return an error.
func TestCELEvaluator_CompileError(t *testing.T) {
	e := newTestEvaluator(t)
	_, err := e.Compile("this is not valid CEL!!!")
	if err == nil {
		t.Error("expected compile error for invalid expression, got nil")
	}
}

// TestCELEvaluator_CompileCache ensures repeated compilations return cached results.
func TestCELEvaluator_CompileCache(t *testing.T) {
	e := newTestEvaluator(t)
	ce1 := compile(t, e, "session.isChild")
	ce2 := compile(t, e, "session.isChild")
	if ce1 != ce2 {
		t.Error("expected cached compiled expression, got different pointers")
	}
}

// TestCELEvaluator_AllContextFields exercises every variable in the context.
func TestCELEvaluator_AllContextFields(t *testing.T) {
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{
		ACP:       ACPContext{Name: "test", Type: "mytype", Tags: []string{"t1"}, AutoApprove: true},
		Workspace: WorkspaceContext{UUID: "wu", Folder: "/ws", Name: "My WS"},
		Session:   SessionContext{ID: "sid", Name: "sname", IsChild: true, IsAutoChild: false, ParentID: "pid"},
		Parent:    ParentContext{Exists: true, Name: "pname", ACPServer: "pacp"},
		Children:  ChildrenContext{Count: 3, Exists: true, Names: []string{"c1"}, ACPServers: []string{"a1"}},
		Tools:     ToolsContext{Available: true, Names: []string{"tool_a", "tool_b"}},
	}

	exprs := []string{
		`acp.name == "test"`,
		`acp.type == "mytype"`,
		`"t1" in acp.tags`,
		`acp.autoApprove`,
		`workspace.uuid == "wu"`,
		`workspace.folder == "/ws"`,
		`workspace.name == "My WS"`,
		`session.id == "sid"`,
		`session.name == "sname"`,
		`session.isChild`,
		`!session.isAutoChild`,
		`session.parentId == "pid"`,
		`parent.exists`,
		`parent.name == "pname"`,
		`parent.acpServer == "pacp"`,
		`children.count == 3`,
		`children.exists`,
		`"c1" in children.names`,
		`"a1" in children.acpServers`,
		`tools.available`,
		`"tool_a" in tools.names`,
		`tools.hasPattern("tool_*")`,
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			ce := compile(t, e, expr)
			got := evaluate(t, e, ce, ctx)
			if !got {
				t.Errorf("expected true for %q", expr)
			}
		})
	}
}
