package config

import (
	"fmt"
	"os"
	"path/filepath"
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
		// !Session.IsChild — hide if this is a child
		{expr: "!Session.IsChild", ctx: rootCtx, want: true},
		{expr: "!Session.IsChild", ctx: childCtx, want: false},

		// Session.IsChild && Parent.Exists — only show in children
		{expr: "Session.IsChild && Parent.Exists", ctx: childCtx, want: true},
		{expr: "Session.IsChild && Parent.Exists", ctx: rootCtx, want: false},

		// "coding" in ACP.Tags — only for coding servers
		{expr: `"coding" in ACP.Tags`, ctx: childCtx, want: true},
		{expr: `"coding" in ACP.Tags`, ctx: rootCtx, want: false},

		// Children.Count > 0 — only if has children
		{expr: "Children.Count > 0", ctx: rootCtx, want: true},
		{expr: "Children.Count > 0", ctx: childCtx, want: false},

		// Tools.HasPattern("github_*") — only if GitHub tools available
		{expr: `Tools.HasPattern("github_*")`, ctx: childCtx, want: true},
		{expr: `Tools.HasPattern("github_*")`, ctx: rootCtx, want: false},

		// Children.MCPCount — only if enough MCP-created children
		{expr: "Children.MCPCount >= 2", ctx: &PromptEnabledContext{
			Children: ChildrenContext{Count: 3, Exists: true, MCPCount: 2},
		}, want: true},
		{expr: "Children.MCPCount >= 2", ctx: &PromptEnabledContext{
			Children: ChildrenContext{Count: 2, Exists: true, MCPCount: 1},
		}, want: false},
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
	ce := compile(t, e, "Session.IsChild")
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
	ce1 := compile(t, e, "Session.IsChild")
	ce2 := compile(t, e, "Session.IsChild")
	if ce1 != ce2 {
		t.Error("expected cached compiled expression, got different pointers")
	}
}

// TestCELEvaluator_ChildrenMCPCountAliasRemoved asserts that the deprecated
// children.mcp_count alias has been removed: compiling any expression that
// references it must now return an "undeclared reference" compile error.
func TestCELEvaluator_ChildrenMCPCountAliasRemoved(t *testing.T) {
	e := newTestEvaluator(t)
	_, err := e.Compile("children.mcp_count >= 2")
	if err == nil {
		t.Error("expected compile error for removed alias children.mcp_count, got nil")
	}
}

// TestCELEvaluator_PermissionsContext validates permissions.* variables in CEL expressions.
func TestCELEvaluator_PermissionsContext(t *testing.T) {
	e := newTestEvaluator(t)

	withPerms := &PromptEnabledContext{
		Session: SessionContext{ID: "sess-1", IsChild: false},
		Permissions: PermissionsContext{
			CanDoIntrospection:         true,
			CanSendPrompt:              true,
			CanPromptUser:              true,
			CanStartConversation:       true,
			CanInteractOtherWorkspaces: false,
			AutoApprovePermissions:     false,
		},
	}

	noPerms := &PromptEnabledContext{
		Session:  SessionContext{ID: "sess-2", IsChild: true, ParentID: "p1"},
		Children: ChildrenContext{Count: 1, Exists: true},
		Permissions: PermissionsContext{
			CanSendPrompt:        false,
			CanStartConversation: false,
		},
	}

	tests := []struct {
		expr string
		ctx  *PromptEnabledContext
		want bool
	}{
		// Basic permissions flag tests
		{expr: "Permissions.CanSendPrompt", ctx: withPerms, want: true},
		{expr: "!Permissions.CanSendPrompt", ctx: noPerms, want: true},
		{expr: "Permissions.CanPromptUser", ctx: withPerms, want: true},
		{expr: "Permissions.CanDoIntrospection", ctx: withPerms, want: true},
		{expr: "Permissions.CanStartConversation", ctx: withPerms, want: true},
		{expr: "!Permissions.CanInteractOtherWorkspaces", ctx: withPerms, want: true},
		{expr: "!Permissions.AutoApprovePermissions", ctx: withPerms, want: true},
		// Combined expressions
		{expr: "Permissions.CanStartConversation && !Session.IsChild", ctx: withPerms, want: true},
		{expr: "Permissions.CanStartConversation && !Session.IsChild", ctx: noPerms, want: false},
		{expr: "Permissions.CanSendPrompt && Children.Exists", ctx: noPerms, want: false},
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

// TestCELConvenienceFunctions validates ACP.MatchesServerType, Tools.HasAllPatterns,
// and Tools.HasAnyPattern CEL convenience functions.
func TestCELConvenienceFunctions(t *testing.T) {
	e := newTestEvaluator(t)

	augCtx := &PromptEnabledContext{
		ACP:   ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
		Tools: ToolsContext{Available: true, Names: []string{"mitto_list", "jira_create_issue", "github_pr"}},
	}
	noACPCtx := &PromptEnabledContext{
		ACP:   ACPContext{Name: "", Type: ""},
		Tools: ToolsContext{Available: true, Names: []string{"mitto_list"}},
	}
	// fetchedEmptyCtx: the tool list has been fetched and is known to be empty
	// (Available: true). Tool-pattern functions evaluate normally and fail closed.
	fetchedEmptyCtx := &PromptEnabledContext{
		ACP:   ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
		Tools: ToolsContext{Available: true, Names: nil},
	}
	// unknownToolsCtx: the tool list has not been fetched yet (Available: false).
	// Tool-pattern functions fail open (return true) so prompts are not hidden
	// during the MCP-tools cache warm-up window.
	unknownToolsCtx := &PromptEnabledContext{
		ACP:   ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
		Tools: ToolsContext{Available: false, Names: nil},
	}

	tests := []struct {
		name string
		expr string
		ctx  *PromptEnabledContext
		want bool
	}{
		// ACP.MatchesServerType — matches type only, not display name
		{"matchesServerType type match", `ACP.MatchesServerType("augment")`, augCtx, true},
		{"matchesServerType display name does not match", `ACP.MatchesServerType("Auggie (Opus 4.6)")`, augCtx, false},
		{"matchesServerType single no match", `ACP.MatchesServerType("claude-code")`, augCtx, false},
		{"matchesServerType case insensitive", `ACP.MatchesServerType("AUGMENT")`, augCtx, true},
		{"matchesServerType fail-open empty acp", `ACP.MatchesServerType("anything")`, noACPCtx, true},

		// ACP.Name model-name fallback (delegate-to-coder / delegate-playwright, mitto-i7n.12).
		// contains() is case-sensitive and misses the real display name "Auggie (Opus 4.6)";
		// the case-insensitive matches() fallback must still fire.
		{"name contains opus is case-sensitive (documents bug)", `ACP.Name.contains("opus")`, augCtx, false},
		{"name matches opus case-insensitive", `ACP.Name.matches("(?i)opus|o3|deep-research|codex")`, augCtx, true},
		{"name matches no model keyword", `ACP.Name.matches("(?i)o3|deep-research|codex")`, augCtx, false},

		// ACP.MatchesServerType — list arg
		{"matchesServerType list one matches", `ACP.MatchesServerType(["augment", "claude-code"])`, augCtx, true},
		{"matchesServerType list none match", `ACP.MatchesServerType(["cursor", "claude-code"])`, augCtx, false},
		{"matchesServerType empty list", `ACP.MatchesServerType([])`, augCtx, false},

		// Tools.HasAllPatterns — single string arg
		{"hasAllPatterns single satisfied", `Tools.HasAllPatterns("mitto_*")`, augCtx, true},
		{"hasAllPatterns single not satisfied", `Tools.HasAllPatterns("slack_*")`, augCtx, false},

		// Tools.HasAllPatterns — list arg
		{"hasAllPatterns list all satisfied", `Tools.HasAllPatterns(["mitto_*", "jira_*"])`, augCtx, true},
		{"hasAllPatterns list some unsatisfied", `Tools.HasAllPatterns(["mitto_*", "slack_*"])`, augCtx, false},
		{"hasAllPatterns fetched-empty fails closed", `Tools.HasAllPatterns(["mitto_*"])`, fetchedEmptyCtx, false},
		{"hasAllPatterns unknown tools fails open", `Tools.HasAllPatterns(["mitto_*"])`, unknownToolsCtx, true},

		// Tools.HasAnyPattern — list arg
		{"hasAnyPattern list one satisfied", `Tools.HasAnyPattern(["slack_*", "jira_*"])`, augCtx, true},
		{"hasAnyPattern list none satisfied", `Tools.HasAnyPattern(["slack_*", "notion_*"])`, augCtx, false},

		// Tools.HasAnyPattern — single string arg
		{"hasAnyPattern single satisfied", `Tools.HasAnyPattern("github_*")`, augCtx, true},
		{"hasAnyPattern fetched-empty fails closed", `Tools.HasAnyPattern(["mitto_*"])`, fetchedEmptyCtx, false},
		{"hasAnyPattern unknown tools fails open", `Tools.HasAnyPattern(["mitto_*"])`, unknownToolsCtx, true},
		{"hasPattern unknown tools fails open", `Tools.HasPattern("mitto_*")`, unknownToolsCtx, true},
		{"hasPattern fetched-empty fails closed", `Tools.HasPattern("mitto_*")`, fetchedEmptyCtx, false},

		// Combined expression
		{"combined matchesServerType and hasAllPatterns",
			`ACP.MatchesServerType("augment") && Tools.HasAllPatterns(["mitto_*", "jira_*"])`,
			augCtx, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			got := evaluate(t, e, ce, tt.ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_CommandExists validates the CommandExists() CEL function.
func TestCELEvaluator_CommandExists(t *testing.T) {
	e := newTestEvaluator(t)

	// Use a minimal context — CommandExists doesn't depend on any context fields
	ctx := &PromptEnabledContext{
		Session: SessionContext{ID: "test"},
	}

	tests := []struct {
		name string
		expr string
		want bool
	}{
		// "ls" should always be available on any Unix/macOS system
		{"available command", `CommandExists("ls")`, true},
		// A nonsense command should not be available
		{"unavailable command", `CommandExists("nonexistent_command_xyz_123456")`, false},
		// Empty string should return false
		{"empty string", `CommandExists("")`, false},
		// Can be combined with other expressions
		{"combined expression", `CommandExists("ls") && !Session.IsChild`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			got := evaluate(t, e, ce, ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_FileExists validates the FileExists() CEL function.
func TestCELEvaluator_FileExists(t *testing.T) {
	e := newTestEvaluator(t)

	// Create a temp directory with a known file and subdirectory
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "testfile.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	testSubDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(testSubDir, 0755); err != nil {
		t.Fatalf("failed to create test subdir: %v", err)
	}

	ctx := &PromptEnabledContext{
		Session:   SessionContext{ID: "test"},
		Workspace: WorkspaceContext{Folder: tmpDir},
	}

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"existing file", `FileExists("testfile.txt")`, true},
		{"existing directory returns false", `FileExists("subdir")`, false},
		{"nonexistent file", `FileExists("no_such_file.xyz")`, false},
		{"empty string", `FileExists("")`, false},
		{"absolute path exists", fmt.Sprintf(`FileExists(%q)`, testFile), true},
		{"absolute path not exists", `FileExists("/nonexistent/path/xyz")`, false},
		{"combined expression", `FileExists("testfile.txt") && !Session.IsChild`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			got := evaluate(t, e, ce, ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_DirExists validates the DirExists() CEL function.
func TestCELEvaluator_DirExists(t *testing.T) {
	e := newTestEvaluator(t)

	// Create a temp directory with a known file and subdirectory
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "testfile.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	testSubDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(testSubDir, 0755); err != nil {
		t.Fatalf("failed to create test subdir: %v", err)
	}

	ctx := &PromptEnabledContext{
		Session:   SessionContext{ID: "test"},
		Workspace: WorkspaceContext{Folder: tmpDir},
	}

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"existing directory", `DirExists("subdir")`, true},
		{"file returns false", `DirExists("testfile.txt")`, false},
		{"nonexistent directory", `DirExists("no_such_dir")`, false},
		{"empty string", `DirExists("")`, false},
		{"absolute path exists", fmt.Sprintf(`DirExists(%q)`, testSubDir), true},
		{"absolute path not exists", `DirExists("/nonexistent/path/xyz")`, false},
		{"combined expression", `DirExists("subdir") && !Session.IsChild`, true},
		{"file and dir combined", `FileExists("testfile.txt") && DirExists("subdir")`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			got := evaluate(t, e, ce, ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_AllContextFields exercises every variable in the context.
func TestCELEvaluator_AllContextFields(t *testing.T) {
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{
		ACP:       ACPContext{Name: "test", Type: "mytype", Tags: []string{"t1"}, AutoApprove: true},
		Workspace: WorkspaceContext{UUID: "wu", Folder: "/ws", Name: "My WS"},
		Session:   SessionContext{ID: "sid", Name: "sname", IsChild: true, IsAutoChild: false, ParentID: "pid", IsPeriodicConversation: true, ModelTags: []string{"smart"}},
		Parent:    ParentContext{Exists: true, Name: "pname", ACPServer: "pacp"},
		Children:  ChildrenContext{Count: 3, Exists: true, MCPCount: 2, Names: []string{"c1"}, ACPServers: []string{"a1"}, PromptingCount: 1, IdleCount: 2},
		Tools:     ToolsContext{Available: true, Names: []string{"tool_a", "tool_b"}},
		Permissions: PermissionsContext{
			CanDoIntrospection:         true,
			CanSendPrompt:              true,
			CanPromptUser:              true,
			CanStartConversation:       true,
			CanInteractOtherWorkspaces: true,
			AutoApprovePermissions:     true,
		},
	}

	exprs := []string{
		`ACP.Name == "test"`,
		`ACP.Type == "mytype"`,
		`"t1" in ACP.Tags`,
		`ACP.AutoApprove`,
		`Workspace.UUID == "wu"`,
		`Workspace.Folder == "/ws"`,
		`Workspace.Name == "My WS"`,
		`Session.ID == "sid"`,
		`Session.Name == "sname"`,
		`Session.IsChild`,
		`!Session.IsAutoChild`,
		`Session.ParentID == "pid"`,
		`Session.IsPeriodicConversation`,
		`"smart" in Session.ModelTags`,
		`Session.HasModelTag("smart")`,
		`Parent.Exists`,
		`Parent.Name == "pname"`,
		`Parent.ACPServer == "pacp"`,
		`Children.Count == 3`,
		`Children.Exists`,
		`Children.MCPCount == 2`,
		`"c1" in Children.Names`,
		`"a1" in Children.ACPServers`,
		`Children.PromptingCount == 1`,
		`Children.IdleCount == 2`,
		`Tools.Available`,
		`"tool_a" in Tools.Names`,
		`Tools.HasPattern("tool_*")`,
		`CommandExists("ls")`,
		`Permissions.CanDoIntrospection`,
		`Permissions.CanSendPrompt`,
		`Permissions.CanPromptUser`,
		`Permissions.CanStartConversation`,
		`Permissions.CanInteractOtherWorkspaces`,
		`Permissions.AutoApprovePermissions`,
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

// TestCELEvaluator_SessionIsPeriodicConversation validates the Session.IsPeriodicConversation variable.
func TestCELEvaluator_SessionIsPeriodicConversation(t *testing.T) {
	e := newTestEvaluator(t)
	ce := compile(t, e, "Session.IsPeriodicConversation")

	trueCtx := &PromptEnabledContext{
		Session: SessionContext{IsPeriodicConversation: true},
	}
	if got := evaluate(t, e, ce, trueCtx); !got {
		t.Error("expected true when IsPeriodicConversation=true")
	}

	falseCtx := &PromptEnabledContext{
		Session: SessionContext{IsPeriodicConversation: false},
	}
	if got := evaluate(t, e, ce, falseCtx); got {
		t.Error("expected false when IsPeriodicConversation=false")
	}
}

// TestCELEvaluator_SessionHasModelTag validates the Session.HasModelTag(tag) macro and the
// "tag" in Session.ModelTags membership expression (mitto-i5sr), including case-insensitivity
// and the empty / unknown-model fallback.
func TestCELEvaluator_SessionHasModelTag(t *testing.T) {
	e := newTestEvaluator(t)

	smartCtx := &PromptEnabledContext{
		Session: SessionContext{ModelTags: []string{"Smart", "Expensive"}},
	}
	emptyCtx := &PromptEnabledContext{
		Session: SessionContext{ModelTags: nil},
	}

	tests := []struct {
		name string
		expr string
		ctx  *PromptEnabledContext
		want bool
	}{
		{"macro exact", `Session.HasModelTag("Smart")`, smartCtx, true},
		{"macro case insensitive", `Session.HasModelTag("smart")`, smartCtx, true},
		{"macro miss", `Session.HasModelTag("cheap")`, smartCtx, false},
		{"macro empty tags", `Session.HasModelTag("smart")`, emptyCtx, false},
		{"in operator hit", `"Smart" in Session.ModelTags`, smartCtx, true},
		{"in operator miss", `"cheap" in Session.ModelTags`, smartCtx, false},
		{"combined with negation", `Session.HasModelTag("smart") && !Session.HasModelTag("cheap")`, smartCtx, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			if got := evaluate(t, e, ce, tt.ctx); got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_SessionIsPeriodicForced validates the Session.IsPeriodicForced variable.
func TestCELEvaluator_SessionIsPeriodicForced(t *testing.T) {
	e := newTestEvaluator(t)
	ce := compile(t, e, "Session.IsPeriodicForced")

	trueCtx := &PromptEnabledContext{
		Session: SessionContext{IsPeriodicForced: true},
	}
	if got := evaluate(t, e, ce, trueCtx); !got {
		t.Error("expected true when IsPeriodicForced=true")
	}

	falseCtx := &PromptEnabledContext{
		Session: SessionContext{IsPeriodicForced: false},
	}
	if got := evaluate(t, e, ce, falseCtx); got {
		t.Error("expected false when IsPeriodicForced=false")
	}
}

// TestCELEvaluator_ReferencesItem validates static detection of the item.* namespace.
// List endpoints use this to keep single-pass behavior for prompts that don't depend
// on per-row item data.
func TestCELEvaluator_ReferencesItem(t *testing.T) {
	e := newTestEvaluator(t)

	tests := []struct {
		expr string
		want bool
	}{
		// References item.* — must be detected.
		{`Item.Status == "open"`, true},
		{`Session.IsChild && Item.Priority == "P0"`, true},
		{`Item.Id != ""`, true},
		{`has(Item.Kind)`, true},
		// Does NOT reference item.* — must not be detected.
		{`Session.IsChild`, false},
		{`Tools.HasPattern("github_*")`, false},
		{`ACP.MatchesServerType("augment") && Children.Count > 0`, false},
		{`FileExists(".git/config")`, false},
		// "item" only as part of an unrelated string/identifier must not trigger.
		{`ACP.Name == "item"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			if got := ce.ReferencesItem(); got != tt.want {
				t.Errorf("ReferencesItem(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_ItemContext validates that item.* fields in the activation are
// populated from PromptEnabledContext.Item and that per-row expressions evaluate
// correctly. ReferencesItem must be true for any expression that touches item.*.
func TestCELEvaluator_ItemContext(t *testing.T) {
	e := newTestEvaluator(t)

	closedCtx := &PromptEnabledContext{
		Item: ItemContext{
			Id:       "mitto-abc",
			Status:   "closed",
			Type:     "task",
			Priority: "2",
			Kind:     "beadsIssue",
		},
	}
	openCtx := &PromptEnabledContext{
		Item: ItemContext{
			Id:       "mitto-xyz",
			Status:   "open",
			Type:     "feature",
			Priority: "1",
			Kind:     "beadsIssue",
		},
	}
	emptyCtx := &PromptEnabledContext{} // Item fields all zero-valued

	tests := []struct {
		name           string
		expr           string
		ctx            *PromptEnabledContext
		want           bool
		wantReferences bool
	}{
		// Item.Status checks
		{"closed hides when closed", `Item.Status != "closed"`, closedCtx, false, true},
		{"open passes when open", `Item.Status != "closed"`, openCtx, true, true},
		{"empty status passes", `Item.Status != "closed"`, emptyCtx, true, true},

		// Item.Kind check
		{"kind matches", `Item.Kind == "beadsIssue"`, closedCtx, true, true},
		{"kind empty on empty ctx", `Item.Kind == "beadsIssue"`, emptyCtx, false, true},

		// Item.Id check
		{"id non-empty", `Item.Id != ""`, closedCtx, true, true},
		{"id empty on empty ctx", `Item.Id != ""`, emptyCtx, false, true},

		// Item.Type check
		{"type matches feature", `Item.Type == "feature"`, openCtx, true, true},
		{"type does not match task", `Item.Type == "task"`, openCtx, false, true},

		// Item.Priority check
		{"priority string match", `Item.Priority == "1"`, openCtx, true, true},
		{"priority no match", `Item.Priority == "0"`, openCtx, false, true},

		// Combined with session
		{"item and session combined", `Item.Status != "closed" && !Session.IsChild`, openCtx, true, true},

		// Non-item expression must have ReferencesItem=false
		{"non-item expr not detected", `Session.IsChild`, openCtx, false, false},
		{"acp expr not detected", `ACP.Name == ""`, openCtx, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := compile(t, e, tt.expr)
			if ce.ReferencesItem() != tt.wantReferences {
				t.Errorf("ReferencesItem(%q) = %v, want %v", tt.expr, ce.ReferencesItem(), tt.wantReferences)
			}
			got := evaluate(t, e, ce, tt.ctx)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestCELEvaluator_AnalyzeLogsEnabledWhen is a regression test for mitto-vjos.1.
// It pins the exact literal expression used by the "Analyze Logs" prompt
// (CommandExists("bd") && DirExists(".beads")) so that a future CEL migration
// cannot silently re-break this prompt's gate.
func TestCELEvaluator_AnalyzeLogsEnabledWhen(t *testing.T) {
	e := newTestEvaluator(t)

	// Create a temp workspace that contains a .beads subdirectory.
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".beads"), 0755); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	ctx := &PromptEnabledContext{
		Session:   SessionContext{ID: "test"},
		Workspace: WorkspaceContext{Folder: tmpDir},
	}

	// Core regression assertion: the exact prompt expression must compile without error.
	const analyzeLogsExpr = `CommandExists("bd") && DirExists(".beads")`
	ce := compile(t, e, analyzeLogsExpr)

	// Evaluation must not return an error regardless of whether "bd" is on PATH.
	evaluate(t, e, ce, ctx)

	// Deterministic variant: "ls" is always available; .beads dir exists → must be true.
	const deterministicExpr = `CommandExists("ls") && DirExists(".beads")`
	ce2 := compile(t, e, deterministicExpr)
	if got := evaluate(t, e, ce2, ctx); !got {
		t.Errorf("Evaluate(%q) = false, want true (.beads dir exists and ls is always on PATH)", deterministicExpr)
	}
}

// benchEvalCtx is a representative context exercising tools/ACP/workspace functions.
var benchEvalCtx = &PromptEnabledContext{
	ACP:      ACPContext{Name: "Auggie (Opus)", Type: "augment", Tags: []string{"coding", "fast"}},
	Session:  SessionContext{ID: "s1", IsChild: true, ParentID: "p1"},
	Parent:   ParentContext{Exists: true, Name: "Parent", ACPServer: "augment"},
	Children: ChildrenContext{Count: 2, Exists: true},
	Tools:    ToolsContext{Available: true, Names: []string{"github_create_pr", "github_list_issues", "mitto_list"}},
}

// BenchmarkEvaluate measures the per-evaluation cost after the program is compiled
// and cached. Previously Evaluate called env.Extend + env.Program on every call;
// now it reuses the cached cel.Program, so this reflects pure evaluation cost.
func BenchmarkEvaluate(b *testing.B) {
	e, err := NewCELEvaluator()
	if err != nil {
		b.Fatalf("NewCELEvaluator: %v", err)
	}
	ce, err := e.Compile(`Session.IsChild && Parent.Exists && ACP.MatchesServerType("augment") && Tools.HasAllPatterns(["github_*", "mitto_*"])`)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Evaluate(ce, benchEvalCtx); err != nil {
			b.Fatalf("Evaluate: %v", err)
		}
	}
}

// BenchmarkCompileAndEvaluate measures a cold compile followed by an evaluation,
// for comparison against the cached-program path in BenchmarkEvaluate.
func BenchmarkCompileAndEvaluate(b *testing.B) {
	const expr = `Session.IsChild && Parent.Exists && ACP.MatchesServerType("augment") && Tools.HasAllPatterns(["github_*", "mitto_*"])`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e, err := NewCELEvaluator()
		if err != nil {
			b.Fatalf("NewCELEvaluator: %v", err)
		}
		ce, err := e.Compile(expr)
		if err != nil {
			b.Fatalf("Compile: %v", err)
		}
		if _, err := e.Evaluate(ce, benchEvalCtx); err != nil {
			b.Fatalf("Evaluate: %v", err)
		}
	}
}


// TestCELEvaluator_UserData validates UserData["x"] and "x" in UserData.
func TestCELEvaluator_UserData(t *testing.T) {
	e := newTestEvaluator(t)

	ctx := &PromptEnabledContext{
		UserData: map[string]string{
			"JIRA Ticket": "PROJ-42",
		},
	}

	tests := []struct {
		expr string
		ctx  *PromptEnabledContext
		want bool
	}{
		// key present: membership test
		{`"JIRA Ticket" in UserData`, ctx, true},
		// key present: value comparison
		{`UserData["JIRA Ticket"] == "PROJ-42"`, ctx, true},
		// key absent
		{`"missing" in UserData`, ctx, false},
		// nil UserData (menu time) — normalized to empty map; must not error
		{`"x" in UserData`, &PromptEnabledContext{}, false},
		// empty map — absent key not in map
		{`"x" in UserData`, &PromptEnabledContext{UserData: map[string]string{}}, false},
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
