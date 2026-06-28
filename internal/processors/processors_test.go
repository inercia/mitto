package processors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	rootconfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/config"
)

func TestBuildCELContext_ArgsAndPeriodicForced(t *testing.T) {
	input := &ProcessorInput{
		SessionID:        "sess-1",
		IsPeriodicForced: true,
		Arguments:        map[string]string{"BRANCH": "main"},
	}
	ctx := BuildCELContext(input)
	if !ctx.Session.IsPeriodicForced {
		t.Error("expected ctx.Session.IsPeriodicForced=true")
	}
	if ctx.Args == nil || ctx.Args["BRANCH"] != "main" {
		t.Fatalf("expected ctx.Args populated from input.Arguments, got %#v", ctx.Args)
	}

	// nil Arguments (menu-time shape) must yield a nil-safe Args map.
	empty := BuildCELContext(&ProcessorInput{SessionID: "sess-2"})
	if empty.Args != nil {
		t.Errorf("expected nil Args when input.Arguments is nil, got %#v", empty.Args)
	}
	if empty.Session.IsPeriodicForced {
		t.Error("expected IsPeriodicForced=false by default")
	}
}

// TestBuildCELContext_NewFields asserts that BuildCELContext populates the new
// fields added in mitto-jkpn: ACP.Available, Children.All, Children.MCP,
// Session.UserDataJSON, and Workspace.UserDataSchemaJSON.
func TestBuildCELContext_NewFields(t *testing.T) {
	input := &ProcessorInput{
		SessionID: "sess-1",
		ACPServer: "auggie",
		AvailableACPServers: []AvailableACPServer{
			{Name: "auggie", Type: "augment", Tags: []string{"coding"}, Current: true},
			{Name: "claude", Type: "claude-code", Tags: []string{"fast"}, Current: false},
		},
		ChildSessions: []ChildSession{
			{ID: "c1", Name: "Coder", ACPServer: "auggie", ChildOrigin: "mcp", IsPrompting: true},
			{ID: "c2", Name: "Helper", ACPServer: "claude", ChildOrigin: "auto", IsPrompting: false},
		},
		UserDataJSON:       `[{"name":"env","value":"prod"}]`,
		UserDataSchemaJSON: `[{"name":"env","type":"string"}]`,
	}

	ctx := BuildCELContext(input)

	// ACP.Available
	if len(ctx.ACP.Available) != 2 {
		t.Fatalf("ACP.Available: expected 2 entries, got %d", len(ctx.ACP.Available))
	}
	if ctx.ACP.Available[0].Name != "auggie" || !ctx.ACP.Available[0].Current {
		t.Errorf("ACP.Available[0]: got %+v", ctx.ACP.Available[0])
	}
	if ctx.ACP.Available[1].Name != "claude" || ctx.ACP.Available[1].Current {
		t.Errorf("ACP.Available[1]: got %+v", ctx.ACP.Available[1])
	}

	// Children.All — both children
	if len(ctx.Children.All) != 2 {
		t.Fatalf("Children.All: expected 2, got %d", len(ctx.Children.All))
	}
	if ctx.Children.All[0].ID != "c1" || !ctx.Children.All[0].IsPrompting {
		t.Errorf("Children.All[0]: got %+v", ctx.Children.All[0])
	}
	if ctx.Children.All[1].ID != "c2" || ctx.Children.All[1].IsPrompting {
		t.Errorf("Children.All[1]: got %+v", ctx.Children.All[1])
	}

	// Children.MCP — only the mcp child
	if len(ctx.Children.MCP) != 1 {
		t.Fatalf("Children.MCP: expected 1, got %d", len(ctx.Children.MCP))
	}
	if ctx.Children.MCP[0].ID != "c1" || ctx.Children.MCP[0].Origin != "mcp" {
		t.Errorf("Children.MCP[0]: got %+v", ctx.Children.MCP[0])
	}

	// Session.UserDataJSON
	if ctx.Session.UserDataJSON != input.UserDataJSON {
		t.Errorf("Session.UserDataJSON = %q, want %q", ctx.Session.UserDataJSON, input.UserDataJSON)
	}

	// Workspace.UserDataSchemaJSON
	if ctx.Workspace.UserDataSchemaJSON != input.UserDataSchemaJSON {
		t.Errorf("Workspace.UserDataSchemaJSON = %q, want %q", ctx.Workspace.UserDataSchemaJSON, input.UserDataSchemaJSON)
	}
}

// TestBuildCELContext_UserData asserts that BuildCELContext populates ctx.UserData
// from input.UserData (name→value map).
func TestBuildCELContext_UserData(t *testing.T) {
	input := &ProcessorInput{
		SessionID: "sess-1",
		UserData:  map[string]string{"JIRA Ticket": "PROJ-42", "env": "prod"},
	}
	ctx := BuildCELContext(input)

	if ctx.UserData == nil {
		t.Fatal("expected ctx.UserData to be populated, got nil")
	}
	if ctx.UserData["JIRA Ticket"] != "PROJ-42" {
		t.Errorf(`ctx.UserData["JIRA Ticket"] = %q, want "PROJ-42"`, ctx.UserData["JIRA Ticket"])
	}
	if ctx.UserData["env"] != "prod" {
		t.Errorf(`ctx.UserData["env"] = %q, want "prod"`, ctx.UserData["env"])
	}

	// nil input.UserData must yield nil ctx.UserData (safe to index).
	emptyCtx := BuildCELContext(&ProcessorInput{SessionID: "s"})
	if emptyCtx.UserData != nil {
		t.Errorf("expected nil ctx.UserData when input.UserData is nil, got %#v", emptyCtx.UserData)
	}
}

// TestBuildCELContext_EmptyInput verifies no panics and zero values for new fields
// when input has no ACP servers, no children, and no user-data JSON.
func TestBuildCELContext_EmptyInput(t *testing.T) {
	ctx := BuildCELContext(&ProcessorInput{SessionID: "s"})
	if len(ctx.ACP.Available) != 0 {
		t.Errorf("expected empty ACP.Available, got %d", len(ctx.ACP.Available))
	}
	if len(ctx.Children.All) != 0 {
		t.Errorf("expected empty Children.All, got %d", len(ctx.Children.All))
	}
	if len(ctx.Children.MCP) != 0 {
		t.Errorf("expected empty Children.MCP, got %d", len(ctx.Children.MCP))
	}
	if ctx.Session.UserDataJSON != "" {
		t.Errorf("expected empty Session.UserDataJSON, got %q", ctx.Session.UserDataJSON)
	}
	if ctx.Workspace.UserDataSchemaJSON != "" {
		t.Errorf("expected empty Workspace.UserDataSchemaJSON, got %q", ctx.Workspace.UserDataSchemaJSON)
	}
}

func TestProcessorIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		enabled  *bool
		expected bool
	}{
		{"nil (default)", nil, true},
		{"true", boolPtr(true), true},
		{"false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Processor{Enabled: tt.enabled}
			if got := h.IsEnabled(); got != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestProcessorGetters(t *testing.T) {
	// Test defaults
	h := &Processor{}
	if got := h.GetTimeout(); got != Duration(DefaultTimeout) {
		t.Errorf("GetTimeout() = %v, want %v", got, DefaultTimeout)
	}
	if got := h.GetPriority(); got != DefaultPriority {
		t.Errorf("GetPriority() = %v, want %v", got, DefaultPriority)
	}
	if got := h.GetInput(); got != DefaultInput {
		t.Errorf("GetInput() = %v, want %v", got, DefaultInput)
	}
	if got := h.GetOutput(); got != DefaultOutput {
		t.Errorf("GetOutput() = %v, want %v", got, DefaultOutput)
	}
	if got := h.GetWorkingDir(); got != DefaultWorkingDir {
		t.Errorf("GetWorkingDir() = %v, want %v", got, DefaultWorkingDir)
	}
	if got := h.GetOnError(); got != DefaultErrorHandle {
		t.Errorf("GetOnError() = %v, want %v", got, DefaultErrorHandle)
	}

	// Test custom values
	h2 := &Processor{
		Timeout:    Duration(10 * time.Second),
		Priority:   50,
		Input:      InputConversation,
		Output:     OutputAppend,
		WorkingDir: WorkingDirHook,
		OnError:    ErrorFail,
	}
	if got := h2.GetTimeout(); got != Duration(10*time.Second) {
		t.Errorf("GetTimeout() = %v, want %v", got, 10*time.Second)
	}
	if got := h2.GetPriority(); got != 50 {
		t.Errorf("GetPriority() = %v, want 50", got)
	}
}

func TestProcessorShouldApply(t *testing.T) {
	tests := []struct {
		name           string
		hook           *Processor
		isFirstMessage bool
		input          *ProcessorInput
		expected       bool
	}{
		{
			name:           "disabled hook",
			hook:           &Processor{Enabled: boolPtr(false), When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "when=first, is first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchFirst}},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "when=first, not first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchFirst}},
			isFirstMessage: false,
			expected:       false,
		},
		{
			name:           "when=all, is first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "when=all, not first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}},
			isFirstMessage: false,
			expected:       true,
		},
		{
			name:           "when=all-except-first, is first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAllExceptFirst}},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "when=all-except-first, not first",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAllExceptFirst}},
			isFirstMessage: false,
			expected:       true,
		},
		{
			name: "enabledWhen CEL matches ACP.Name",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `ACP.Name == "auggie-opus"`},
			input: &ProcessorInput{
				ACPServer: "auggie-opus",
				AvailableACPServers: []AvailableACPServer{
					{Name: "auggie-opus", Type: "auggie", Current: true},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL ACP.Name no match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `ACP.Name == "auggie-opus"`},
			input: &ProcessorInput{
				ACPServer: "auggie-fast",
				AvailableACPServers: []AvailableACPServer{
					{Name: "auggie-fast", Type: "auggie", Current: true},
				},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL matches ACP.Tags",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `ACP.Tags.exists(t, t == "reasoning")`},
			input: &ProcessorInput{
				ACPServer: "auggie-opus",
				AvailableACPServers: []AvailableACPServer{
					{Name: "auggie-opus", Type: "auggie", Tags: []string{"reasoning", "slow"}, Current: true},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL tags no match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `ACP.Tags.exists(t, t == "reasoning")`},
			input: &ProcessorInput{
				ACPServer: "auggie-fast",
				AvailableACPServers: []AvailableACPServer{
					{Name: "auggie-fast", Type: "auggie", Tags: []string{"coding", "fast"}, Current: true},
				},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL Children.Exists",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.Exists`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Sub task"},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "enabledWhen CEL Children.Exists false",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.Exists`},
			input:          &ProcessorInput{},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL Children.MCPCount threshold met",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.MCPCount >= 2`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Task A", ChildOrigin: "mcp"},
					{ID: "child-2", Name: "Task B", ChildOrigin: "mcp"},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL Children.MCPCount below threshold",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.MCPCount >= 2`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Task A", ChildOrigin: "mcp"},
					{ID: "child-2", Name: "Auto child", ChildOrigin: "auto"},
				},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL Children.PromptingCount zero when none prompting",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.PromptingCount == 0`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Task A", ChildOrigin: "mcp", IsPrompting: false},
					{ID: "child-2", Name: "Task B", ChildOrigin: "mcp", IsPrompting: false},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL Children.PromptingCount non-zero when child is prompting",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.PromptingCount == 0`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Task A", ChildOrigin: "mcp", IsPrompting: true},
					{ID: "child-2", Name: "Task B", ChildOrigin: "mcp", IsPrompting: false},
				},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL Children.IdleCount correct",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Children.IdleCount == 1`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Task A", ChildOrigin: "mcp", IsPrompting: true},
					{ID: "child-2", Name: "Task B", ChildOrigin: "mcp", IsPrompting: false},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL invalid expression fails open",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `!!!invalid`},
			input: &ProcessorInput{
				ACPServer: "test",
			},
			isFirstMessage: true,
			expected:       true, // fail-open
		},
		{
			name:           "enabledWhen empty means all",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: ""},
			input:          &ProcessorInput{ACPServer: "anything"},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "Tools.HasAllPatterns all patterns satisfied",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasAllPatterns(["mitto_*", "jira_*"])`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list", "jira_search"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "Tools.HasAllPatterns some patterns not satisfied",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasAllPatterns(["mitto_*", "slack_*"])`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "jira_search"},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "Tools.HasPattern no tools available",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasPattern("mitto_*")`},
			input:          &ProcessorInput{MCPToolNames: []string{}},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen empty tools means all",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: ""},
			input: &ProcessorInput{
				MCPToolNames: []string{"anything"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "Tools.HasPattern exact tool match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasPattern("mitto_conversation_new")`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL Tools.HasPattern",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasPattern("mitto_*")`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL Tools.HasPattern no match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `Tools.HasPattern("slack_*")`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "jira_search"},
			},
			isFirstMessage: true,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := tt.hook.ShouldApply(tt.isFirstMessage, tt.input)
			if got != tt.expected {
				t.Errorf("ShouldApply() = %v (reason=%s), want %v", got, reason, tt.expected)
			}
		})
	}
}

func TestManagerApply_RerunAfterTime(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "ctx.yaml", `
name: context-injector
enabled: true
when:
  on: userPrompt
  match: first
  rerun:
    afterTime: 50ms
mutate: prepend
text: "[context]"
`)

	mgr := NewManager(dir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx := context.Background()

	// First message — should apply
	input := &ProcessorInput{Message: "msg1", IsFirstMessage: true}
	result, err := mgr.Apply(ctx, input)
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	if !strings.Contains(result.Message, "[context]") {
		t.Errorf("first apply should prepend context, got %q", result.Message)
	}

	// Second message (not first) — should NOT apply (rerun not due)
	input2 := &ProcessorInput{Message: "msg2", IsFirstMessage: false}
	result2, err := mgr.Apply(ctx, input2)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if strings.Contains(result2.Message, "[context]") {
		t.Errorf("second apply should NOT have context yet, got %q", result2.Message)
	}

	// Wait for rerun threshold
	time.Sleep(60 * time.Millisecond)

	// Third message — should re-apply (time threshold reached)
	input3 := &ProcessorInput{Message: "msg3", IsFirstMessage: false}
	result3, err := mgr.Apply(ctx, input3)
	if err != nil {
		t.Fatalf("Apply 3: %v", err)
	}
	if !strings.Contains(result3.Message, "[context]") {
		t.Errorf("third apply should have context (rerun), got %q", result3.Message)
	}
}

func TestManagerApply_RerunAfterMsgs(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "ctx.yaml", `
name: context-injector
enabled: true
when:
  on: userPrompt
  match: first
  rerun:
    afterSentMsgs: 3
mutate: prepend
text: "[context]"
`)

	mgr := NewManager(dir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx := context.Background()

	// Message 1 (first) — applies
	input := &ProcessorInput{Message: "msg1", IsFirstMessage: true}
	result, _ := mgr.Apply(ctx, input)
	if !strings.Contains(result.Message, "[context]") {
		t.Fatalf("first apply should prepend context")
	}

	// Messages 2-4 — should NOT apply (counter: 1, 2, 3)
	for i := 2; i <= 4; i++ {
		inp := &ProcessorInput{Message: fmt.Sprintf("msg%d", i), IsFirstMessage: false}
		res, _ := mgr.Apply(ctx, inp)
		if i < 5 && strings.Contains(res.Message, "[context]") {
			t.Errorf("msg%d should NOT have context, got %q", i, res.Message)
		}
	}

	// Message 5 — should re-apply (3 messages since last run)
	input5 := &ProcessorInput{Message: "msg5", IsFirstMessage: false}
	result5, _ := mgr.Apply(ctx, input5)
	if !strings.Contains(result5.Message, "[context]") {
		t.Errorf("msg5 should have context (rerun after 3 msgs), got %q", result5.Message)
	}
}

func TestRerunConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RerunConfig
		wantErr bool
	}{
		{name: "nil config", cfg: nil, wantErr: false},
		{name: "valid time", cfg: &RerunConfig{AfterTime: "10m"}, wantErr: false},
		{name: "valid msgs", cfg: &RerunConfig{AfterSentMsgs: 5}, wantErr: false},
		{name: "both set", cfg: &RerunConfig{AfterTime: "1h", AfterSentMsgs: 20}, wantErr: false},
		{name: "invalid time", cfg: &RerunConfig{AfterTime: "invalid"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManagerApply_RerunAfterTokens(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "ctx.yaml", `
name: context-injector
enabled: true
when:
  on: userPrompt
  match: first
  rerun:
    afterTokens: 1000
text: "[CONTEXT]"
mutate: prepend
`)

	mgr := NewManager(dir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	ctx := context.Background()

	// First message — should apply (is first)
	input1 := &ProcessorInput{Message: "msg1", IsFirstMessage: true}
	result1, err := mgr.Apply(ctx, input1)
	if err != nil {
		t.Fatalf("Apply 1 failed: %v", err)
	}
	if !strings.Contains(result1.Message, "[CONTEXT]") {
		t.Errorf("first apply should have context, got %q", result1.Message)
	}

	// Accumulate 500 tokens (below threshold)
	mgr.AccumulateTokenUsage(500)

	// Second message — should NOT apply (tokens below threshold)
	input2 := &ProcessorInput{Message: "msg2", IsFirstMessage: false}
	result2, err := mgr.Apply(ctx, input2)
	if err != nil {
		t.Fatalf("Apply 2 failed: %v", err)
	}
	if strings.Contains(result2.Message, "[CONTEXT]") {
		t.Errorf("second apply should NOT have context (500 tokens < 1000), got %q", result2.Message)
	}

	// Accumulate 600 more tokens (total 1100, above threshold)
	mgr.AccumulateTokenUsage(600)

	// Third message — should re-apply (token threshold reached)
	input3 := &ProcessorInput{Message: "msg3", IsFirstMessage: false}
	result3, err := mgr.Apply(ctx, input3)
	if err != nil {
		t.Fatalf("Apply 3 failed: %v", err)
	}
	if !strings.Contains(result3.Message, "[CONTEXT]") {
		t.Errorf("third apply should have context (rerun after 1100 tokens >= 1000), got %q", result3.Message)
	}

	// Fourth message — should NOT apply (tokens reset after rerun)
	input4 := &ProcessorInput{Message: "msg4", IsFirstMessage: false}
	result4, err := mgr.Apply(ctx, input4)
	if err != nil {
		t.Fatalf("Apply 4 failed: %v", err)
	}
	if strings.Contains(result4.Message, "[CONTEXT]") {
		t.Errorf("fourth apply should NOT have context (tokens reset), got %q", result4.Message)
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "empty string", input: "", expected: 0},
		{name: "single char", input: "a", expected: 1},
		{name: "four chars", input: "abcd", expected: 1},
		{name: "five chars", input: "abcde", expected: 2},
		{name: "eight chars", input: "abcdefgh", expected: 2},
		{name: "typical short message", input: "Hello, world!", expected: 4}, // 13 chars → (13+3)/4 = 4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.expected {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestAccumulateTokenUsage(t *testing.T) {
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{
		{
			Name: "tracked",
			When: WhenConfig{On: PhaseUserPrompt, Match: MatchFirst, Rerun: &RerunConfig{AfterTokens: 100}},
			Text: "test",
		},
		{
			Name: "no-rerun",
			When: WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
			Text: "test2",
		},
	}

	// Initialize rerun state by doing first apply
	ctx := context.Background()
	input := &ProcessorInput{Message: "init", IsFirstMessage: true}
	_, _ = mgr.Apply(ctx, input)

	// Accumulate tokens
	mgr.AccumulateTokenUsage(50)
	mgr.AccumulateTokenUsage(30)

	// Check state
	state := mgr.rerunState["tracked"]
	if state == nil {
		t.Fatal("expected rerun state for 'tracked'")
	}
	if state.tokensSince != 80 {
		t.Errorf("expected tokensSince=80, got %d", state.tokensSince)
	}

	// Zero/negative tokens should be ignored
	mgr.AccumulateTokenUsage(0)
	mgr.AccumulateTokenUsage(-10)
	if state.tokensSince != 80 {
		t.Errorf("expected tokensSince still 80 after zero/negative, got %d", state.tokensSince)
	}
}

func TestRerunConfig_Validate_WithTokens(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RerunConfig
		wantErr bool
	}{
		{name: "tokens only", cfg: &RerunConfig{AfterTokens: 5000}, wantErr: false},
		{name: "all three set", cfg: &RerunConfig{AfterTime: "1h", AfterSentMsgs: 20, AfterTokens: 50000}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoader_RerunOnlyWithFirst(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "bad.yaml", `
name: bad-rerun
enabled: true
when:
  on: userPrompt
  match: all
  rerun:
    afterTime: 10m
text: "test"
mutate: prepend
`)

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load should not return error (skips bad files), got: %v", err)
	}
	// The bad processor should have been skipped
	if len(procs) != 0 {
		t.Errorf("expected 0 processors (bad rerun config skipped), got %d", len(procs))
	}
}

// TestLoader_Validation tests all validation rules for the new on/match schema.
func TestLoader_Validation(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		expectSkip  bool // true = processor should be skipped (bad file), false = should load OK
		expectCount int  // expected processor count after load
	}{
		{
			name: "missing when.on rejected",
			yaml: `
name: bad-on
when:
  match: all
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "missing when.match rejected",
			yaml: `
name: bad-match
when:
  on: userPrompt
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "invalid when.on value rejected",
			yaml: `
name: bad-on-val
when:
  on: beforeSend
  match: all
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "kebab-case all-except-first rejected",
			yaml: `
name: bad-kebab
when:
  on: userPrompt
  match: all-except-first
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "camelCase allExceptFirst accepted",
			yaml: `
name: ok-camel
when:
  on: userPrompt
  match: allExceptFirst
command: /bin/echo
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "agentResponded with text rejected",
			yaml: `
name: bad-ar-text
when:
  on: agentResponded
  match: all
text: "some text"
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "agentResponded with mutate rejected",
			yaml: `
name: bad-ar-mutate
when:
  on: agentResponded
  match: all
mutate: prepend
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "agentResponded with rerun rejected",
			yaml: `
name: bad-ar-rerun
when:
  on: agentResponded
  match: all
  rerun:
    afterTime: 10m
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "agentResponded command-mode accepted",
			yaml: `
name: ok-ar-cmd
when:
  on: agentResponded
  match: all
command: /bin/echo
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "agentResponded prompt-mode accepted",
			yaml: `
name: ok-ar-prompt
when:
  on: agentResponded
  match: all
prompt: "Analyze the session."
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "agentIdle command-mode accepted",
			yaml: `
name: ok-idle-cmd
when:
  on: agentIdle
  match: all
command: /bin/echo
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "agentIdle prompt-mode with cadence accepted",
			yaml: `
name: ok-idle-prompt
when:
  on: agentIdle
  match: all
  cadence:
    everyNTurns: 3
prompt: "Update memory."
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "agentIdle with text rejected",
			yaml: `
name: bad-idle-text
when:
  on: agentIdle
  match: all
text: "some text"
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "userPrompt stopReasons rejected",
			yaml: `
name: bad-up-stop
when:
  on: userPrompt
  match: all
  stopReasons: ["end_turn"]
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "userPrompt excludeOrigins rejected",
			yaml: `
name: bad-up-excl
when:
  on: userPrompt
  match: all
  excludeOrigins: ["user"]
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "legacy sent: key rejected",
			yaml: `
name: bad-sent
when:
  sent: all
command: /bin/echo
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "text mode without mutate rejected",
			yaml: `
name: bad-text-no-mutate
when:
  on: userPrompt
  match: all
text: "hello"
`,
			expectSkip:  true,
			expectCount: 0,
		},
		// parameters block tests (mitto-5g2v.1)
		{
			name: "prompt-mode with valid parameters accepted",
			yaml: `
name: ok-params
when:
  on: userPrompt
  match: first
prompt: "Save content to ${filename}."
parameters:
  - name: filename
    type: text
    description: Target filename
    default: AGENTS.md
`,
			expectSkip:  false,
			expectCount: 1,
		},
		{
			name: "prompt-mode parameter missing default rejected",
			yaml: `
name: bad-no-default
when:
  on: userPrompt
  match: first
prompt: "Save content to ${filename}."
parameters:
  - name: filename
    type: text
    description: Target filename
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "prompt-mode parameter unknown type rejected",
			yaml: `
name: bad-unknown-type
when:
  on: userPrompt
  match: first
prompt: "Save content to ${filename}."
parameters:
  - name: filename
    type: unknownType
    default: AGENTS.md
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "prompt-mode duplicate parameter name rejected",
			yaml: `
name: bad-dup-name
when:
  on: userPrompt
  match: first
prompt: "Use ${x} and ${x} again."
parameters:
  - name: x
    type: text
    default: foo
  - name: x
    type: text
    default: bar
`,
			expectSkip:  true,
			expectCount: 0,
		},
		{
			name: "command-mode with parameters rejected",
			yaml: `
name: bad-cmd-params
when:
  on: userPrompt
  match: all
command: /bin/echo
parameters:
  - name: filename
    type: text
    default: AGENTS.md
`,
			expectSkip:  true,
			expectCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeYAML(t, dir, "proc.yaml", tt.yaml)
			loader := NewLoader(dir, nil)
			procs, err := loader.Load()
			if err != nil {
				t.Fatalf("Load() should not return error (bad files skipped), got: %v", err)
			}
			if len(procs) != tt.expectCount {
				t.Errorf("expected %d processors, got %d", tt.expectCount, len(procs))
			}
		})
	}
}

// TestLoader_Errors_ValidationFailure verifies that a processor with a missing
// mandatory `default` is retained as a ProcessorLoadError (not silently dropped)
// and is accessible via Loader.Errors() and Manager.LoadErrors().
func TestLoader_Errors_ValidationFailure(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "bad.yaml", `
name: bad-no-default
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
`)

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() should not return error (validation failures are retained), got: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("expected 0 valid processors, got %d", len(procs))
	}
	errs := loader.Errors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 load error, got %d: %v", len(errs), errs)
	}
	le := errs[0]
	if le.Name != "bad-no-default" {
		t.Errorf("error.Name = %q, want %q", le.Name, "bad-no-default")
	}
	if le.Error == "" {
		t.Error("error.Error must not be empty")
	}
	if le.FilePath == "" {
		t.Error("error.FilePath must not be empty")
	}

	// Manager.LoadErrors() must thread through the same errors.
	mgr := NewManager(dir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Manager.Load() error: %v", err)
	}
	mgrErrs := mgr.LoadErrors()
	if len(mgrErrs) != 1 {
		t.Fatalf("Manager.LoadErrors(): expected 1, got %d", len(mgrErrs))
	}
	if mgrErrs[0].Name != "bad-no-default" {
		t.Errorf("Manager error.Name = %q, want %q", mgrErrs[0].Name, "bad-no-default")
	}
	if mgrErrs[0].Source != ProcessorSourceGlobal {
		t.Errorf("Manager error.Source = %q, want %q", mgrErrs[0].Source, ProcessorSourceGlobal)
	}
}

// TestLoader_Errors_YAMLParseFailure verifies that a file with a YAML syntax error
// is retained as a file-level ProcessorLoadError with an empty Name.
func TestLoader_Errors_YAMLParseFailure(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "bad.yaml", "invalid: yaml: content:")

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() should not return error (bad files are retained), got: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("expected 0 valid processors, got %d", len(procs))
	}
	errs := loader.Errors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 load error, got %d: %v", len(errs), errs)
	}
	le := errs[0]
	if le.Name != "" {
		t.Errorf("file-level error.Name = %q, want empty (file didn't parse)", le.Name)
	}
	if le.Error == "" {
		t.Error("error.Error must not be empty")
	}
}

// TestLoader_Errors_ValidProcNoErrors verifies that a valid processor produces no
// load errors — ensuring the error-collection path doesn't affect the happy path.
func TestLoader_Errors_ValidProcNoErrors(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "ok.yaml", `
name: ok-proc
when:
  on: userPrompt
  match: all
command: /bin/echo
`)
	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(procs) != 1 {
		t.Errorf("expected 1 processor, got %d", len(procs))
	}
	if len(loader.Errors()) != 0 {
		t.Errorf("expected 0 load errors for valid processor, got %d: %v", len(loader.Errors()), loader.Errors())
	}
}

func TestResolveCommand(t *testing.T) {
	h := &Processor{
		Command: "./script.sh",
		HookDir: "/hooks",
	}
	if got := h.ResolveCommand(); got != "/hooks/script.sh" {
		t.Errorf("ResolveCommand() = %v, want /hooks/script.sh", got)
	}

	h2 := &Processor{
		Command: "/usr/bin/echo",
		HookDir: "/hooks",
	}
	if got := h2.ResolveCommand(); got != "/usr/bin/echo" {
		t.Errorf("ResolveCommand() = %v, want /usr/bin/echo", got)
	}
}

func TestLoaderLoad(t *testing.T) {
	// Create temp directory with test hooks
	tmpDir, err := os.MkdirTemp("", "hooks-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid hook file
	hookContent := `
name: test-hook
command: /bin/echo
when:
  on: userPrompt
  match: all
`
	if err := os.WriteFile(filepath.Join(tmpDir, "test.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write hook file: %v", err)
	}

	// Create disabled directory with a hook (should be skipped)
	disabledDir := filepath.Join(tmpDir, "disabled")
	if err := os.MkdirAll(disabledDir, 0755); err != nil {
		t.Fatalf("Failed to create disabled dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(disabledDir, "skip.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write disabled hook file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	hooks, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(hooks) != 1 {
		t.Errorf("Load() returned %d hooks, want 1", len(hooks))
	}

	if hooks[0].Name != "test-hook" {
		t.Errorf("Hook name = %v, want test-hook", hooks[0].Name)
	}
}

func TestLoaderLoadEmpty(t *testing.T) {
	// Test with non-existent directory
	loader := NewLoader("/nonexistent/path", nil)
	hooks, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("Load() returned %d hooks, want 0", len(hooks))
	}
}

func TestLoaderLoadInvalidYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hooks-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create invalid YAML file
	if err := os.WriteFile(filepath.Join(tmpDir, "invalid.yaml"), []byte("invalid: yaml: content:"), 0644); err != nil {
		t.Fatalf("Failed to write invalid file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	hooks, err := loader.Load()
	// Should not error, just skip invalid files
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("Load() returned %d hooks, want 0 (invalid should be skipped)", len(hooks))
	}
}

func TestExecutorPrepareInput(t *testing.T) {
	executor := NewExecutor("/hooks", nil)
	hook := &Processor{Input: InputMessage}
	input := &ProcessorInput{
		Message:        "test message",
		IsFirstMessage: true,
		SessionID:      "session-123",
		WorkingDir:     "/project",
	}

	data, err := executor.prepareInput(hook, input)
	if err != nil {
		t.Fatalf("prepareInput() error = %v", err)
	}

	// Verify JSON contains expected fields
	expected := `"message":"test message"`
	if !contains(string(data), expected) {
		t.Errorf("prepareInput() = %s, want to contain %s", data, expected)
	}
}

func TestApplyProcessorsEmpty(t *testing.T) {
	ctx := context.Background()
	input := &ProcessorInput{Message: "original"}

	result, err := ApplyProcessors(ctx, nil, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result.Message != "original" {
		t.Errorf("ApplyProcessors() = %v, want original", result.Message)
	}
	if len(result.Attachments) != 0 {
		t.Errorf("ApplyProcessors() attachments = %d, want 0", len(result.Attachments))
	}
}

func TestApplyProcessorsTransform(t *testing.T) {
	// Create a temp directory for the hook
	tmpDir := t.TempDir()

	// Create a simple echo script that transforms the message
	scriptPath := filepath.Join(tmpDir, "transform.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "transformed message"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "transform-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputTransform,
			Input:   InputMessage,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original message",
		IsFirstMessage: true,
		SessionID:      "test-session",
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result.Message != "transformed message" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "transformed message")
	}
}

func TestApplyProcessorsPrepend(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "prepend.sh")
	scriptContent := `#!/bin/sh
echo '{"text": "PREFIX: "}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "prepend-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputPrepend,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	expected := "PREFIX: " + wrapUserRequest("original")
	if result.Message != expected {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, expected)
	}
}

func TestApplyProcessorsAppend(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "append.sh")
	scriptContent := `#!/bin/sh
echo '{"text": " :SUFFIX"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "append-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputAppend,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	expected := wrapUserRequest("original") + wrapSystemNotes(" :SUFFIX")
	if result.Message != expected {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, expected)
	}
}

func TestApplyProcessorsDiscard(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that outputs something, but it should be discarded
	scriptPath := filepath.Join(tmpDir, "discard.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "this should be ignored"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "discard-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputDiscard,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	// Discard processors don't transform the message; the first-message wrapping still applies.
	if result.Message != wrapUserRequest("original") {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, wrapUserRequest("original"))
	}
}

func TestApplyProcessorsSkipsNonApplicable(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "first-only.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "first message only"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "first-only-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchFirst}, // Only applies to first message
			Output:  OutputTransform,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: false, // Not first message
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	// Hook should not apply, message unchanged
	if result.Message != "original" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "original")
	}
}

func TestApplyProcessorsErrorSkip(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that fails
	scriptPath := filepath.Join(tmpDir, "fail.sh")
	scriptContent := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "failing-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputTransform,
			OnError: ErrorSkip, // Should skip on error
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() should not error with ErrorSkip, got: %v", err)
	}
	// Failed processor is skipped; first-message wrapping is still applied.
	if result.Message != wrapUserRequest("original") {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, wrapUserRequest("original"))
	}
}

func TestApplyProcessorsErrorFail(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fail.sh")
	scriptContent := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Processor{
		{
			Name:    "failing-hook",
			Command: scriptPath,
			When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Output:  OutputTransform,
			OnError: ErrorFail, // Should fail on error
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	_, err := ApplyProcessors(ctx, hooks, input, tmpDir, nil)
	if err == nil {
		t.Fatal("ApplyProcessors() should error with ErrorFail")
	}
}

func TestApplyProcessorsTextModePrepend(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "text-prepend",
			Text:   "PREFIX: ",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "hello world",
		IsFirstMessage: true,
	}

	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result.Message != "PREFIX: "+wrapUserRequest("hello world") {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "PREFIX: "+wrapUserRequest("hello world"))
	}
}

func TestApplyProcessorsTextModeAppend(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "text-append",
			Text:   " SUFFIX",
			Mutate: config.ProcessorMutateAppend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "hello world",
		IsFirstMessage: true,
	}

	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result.Message != wrapUserRequest("hello world")+wrapSystemNotes(" SUFFIX") {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, wrapUserRequest("hello world")+wrapSystemNotes(" SUFFIX"))
	}
}

func TestApplyProcessorsTextModeChained(t *testing.T) {
	// Multiple text-mode processors applied in order
	procs := []*Processor{
		{
			Name:   "prepend-context",
			Text:   "Context: ",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
		{
			Name:   "append-footer",
			Text:   "\n---\nEnd",
			Mutate: config.ProcessorMutateAppend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "user message",
		IsFirstMessage: true,
	}

	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	expected := "Context: " + wrapUserRequest("user message") + wrapSystemNotes("\n---\nEnd")
	if result.Message != expected {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, expected)
	}
}

func TestApplyProcessorsTextModeFirstOnly(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "first-only",
			Text:   "FIRST: ",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
		},
	}

	ctx := context.Background()

	// First message — should apply
	input := &ProcessorInput{Message: "msg", IsFirstMessage: true}
	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result.Message != "FIRST: "+wrapUserRequest("msg") {
		t.Errorf("first message: got %q, want %q", result.Message, "FIRST: "+wrapUserRequest("msg"))
	}

	// Subsequent message — should NOT apply
	input2 := &ProcessorInput{Message: "msg", IsFirstMessage: false}
	result2, err := ApplyProcessors(ctx, procs, input2, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}
	if result2.Message != "msg" {
		t.Errorf("subsequent message: got %q, want %q", result2.Message, "msg")
	}
}

// TestApplyProcessors_FirstMessageWrapsUserRequest is a regression test for the
// "first user prompt misclassified as setup context" bug. When processors prepend
// session-context and append reminder instructions, a short user message was
// getting buried between the walls of injected text and the agent classified it
// as boilerplate rather than an actionable request.
//
// The fix: on IsFirstMessage=true, ApplyProcessors wraps the original user text
// in <user_request>…</user_request> so the boundary is unambiguous regardless
// of how much text processors inject before and after it.
func TestApplyProcessors_FirstMessageWrapsUserRequest(t *testing.T) {
	msg := "Remove the line numbers in the beads description editor"

	procs := []*Processor{
		{
			Name:   "session-context-like",
			Text:   "[Session Context]\nSession: test-session\nWorking directory: /tmp/project\n---\n",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
		},
		{
			Name:   "reminder",
			Text:   "\n---\n[Reminder]\nDo not forget to close issues when done.",
			Mutate: config.ProcessorMutateAppend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
		},
	}

	ctx := context.Background()

	// First message: user request should be delimited.
	input := &ProcessorInput{Message: msg, IsFirstMessage: true}
	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}

	wrapped := wrapUserRequest(msg)
	if !strings.Contains(result.Message, wrapped) {
		t.Errorf("expected result to contain wrapped user request %q, got %q", wrapped, result.Message)
	}

	// Ordering: [Session Context] < <user_request> < <mitto_system_notes> (contains [Reminder])
	idxCtx := strings.Index(result.Message, "[Session Context]")
	idxReq := strings.Index(result.Message, "<user_request>")
	idxNotesOpen := strings.Index(result.Message, "<mitto_system_notes>")
	idxNotesClose := strings.Index(result.Message, "</mitto_system_notes>")
	idxRem := strings.Index(result.Message, "[Reminder]")
	if idxCtx < 0 || idxReq < 0 || idxRem < 0 {
		t.Fatalf("expected all three sections present; ctx=%d req=%d rem=%d in %q", idxCtx, idxReq, idxRem, result.Message)
	}
	if idxCtx >= idxReq || idxReq >= idxRem {
		t.Errorf("ordering wrong: [Session Context] at %d, <user_request> at %d, [Reminder] at %d", idxCtx, idxReq, idxRem)
	}

	// System-notes wrapping: appended [Reminder] must be inside <mitto_system_notes>.
	if idxNotesOpen < 0 || idxNotesClose < 0 {
		t.Fatalf("expected <mitto_system_notes>…</mitto_system_notes> in first-message result, got %q", result.Message)
	}
	if idxReq >= idxNotesOpen {
		t.Errorf("ordering wrong: <user_request> at %d should be before <mitto_system_notes> at %d", idxReq, idxNotesOpen)
	}
	if idxNotesOpen >= idxRem || idxRem >= idxNotesClose {
		t.Errorf("[Reminder] at %d should be between <mitto_system_notes> (%d) and </mitto_system_notes> (%d)", idxRem, idxNotesOpen, idxNotesClose)
	}

	// Negative case: non-first message must NOT be wrapped with either tag.
	input2 := &ProcessorInput{Message: msg, IsFirstMessage: false}
	result2, err := ApplyProcessors(ctx, procs, input2, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() (non-first) error = %v", err)
	}
	if strings.Contains(result2.Message, "<user_request>") {
		t.Errorf("non-first message should NOT contain <user_request> wrapper, got %q", result2.Message)
	}
	if strings.Contains(result2.Message, "<mitto_system_notes>") {
		t.Errorf("non-first message should NOT contain <mitto_system_notes> wrapper, got %q", result2.Message)
	}
}

// TestApplyProcessorsWithVariableSubstitution simulates the full pipeline
// as used in BackgroundSession.PromptWithMeta: ApplyProcessors followed by
// SubstituteVariables on the result.
func TestApplyProcessorsWithVariableSubstitution(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "inject-context",
			Text:   "Session: @mitto:session_id\nProject: @mitto:working_dir\n\n",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
		},
		{
			Name:   "inject-footer",
			Text:   "\n[agent: @mitto:acp_server]",
			Mutate: config.ProcessorMutateAppend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "Fix the login bug",
		IsFirstMessage: true,
		SessionID:      "sess-001",
		WorkingDir:     "/home/user/myproject",
		ACPServer:      "claude-code",
	}

	// Step 1: Apply processors (text-mode prepend/append)
	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}

	// At this point, @mitto: variables are still unresolved.
	// The user request is wrapped in <user_request> delimiters (first-message protection).
	// The appended footer is wrapped in <mitto_system_notes> (first-message system-notes wrapping).
	expectedBeforeSubst := "Session: @mitto:session_id\nProject: @mitto:working_dir\n\n" +
		wrapUserRequest("Fix the login bug") + wrapSystemNotes("\n[agent: @mitto:acp_server]")
	if result.Message != expectedBeforeSubst {
		t.Errorf("before substitution: got %q, want %q", result.Message, expectedBeforeSubst)
	}

	// Step 2: Substitute variables (as BackgroundSession does).
	// SubstituteVariables runs on the whole assembled string, so @mitto: tokens
	// inside <mitto_system_notes> are substituted the same as before.
	finalMessage := SubstituteVariables(result.Message, input)

	expectedAfterSubst := "Session: sess-001\nProject: /home/user/myproject\n\n" +
		wrapUserRequest("Fix the login bug") + wrapSystemNotes("\n[agent: claude-code]")
	if finalMessage != expectedAfterSubst {
		t.Errorf("after substitution: got %q, want %q", finalMessage, expectedAfterSubst)
	}
}

// TestApplyProcessorsVariablesInUserMessage tests that @mitto: variables
// in the user's own message text are also substituted.
func TestApplyProcessorsVariablesInUserMessage(t *testing.T) {
	// No processors — just the user message with variables
	ctx := context.Background()
	input := &ProcessorInput{
		Message:         "Check work from @mitto:parent_session_id and continue in @mitto:session_id",
		IsFirstMessage:  true,
		SessionID:       "child-session",
		ParentSessionID: "parent-session",
	}

	result, err := ApplyProcessors(ctx, nil, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}

	// ApplyProcessors with nil processors returns message unchanged
	finalMessage := SubstituteVariables(result.Message, input)

	expected := "Check work from parent-session and continue in child-session"
	if finalMessage != expected {
		t.Errorf("got %q, want %q", finalMessage, expected)
	}
}

// TestApplyProcessorsVariablesEmptyValues tests that @mitto: variables
// with empty values substitute to empty string (not left as-is).
func TestApplyProcessorsVariablesEmptyValues(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "context",
			Text:   "Parent: @mitto:parent_session_id\n",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:         "hello",
		IsFirstMessage:  true,
		SessionID:       "sess-001",
		ParentSessionID: "", // No parent
	}

	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}

	finalMessage := SubstituteVariables(result.Message, input)

	// Empty parent_session_id should substitute to empty string.
	// The user request is wrapped in <user_request> delimiters (first-message protection).
	expected := "Parent: \n" + wrapUserRequest("hello")
	if finalMessage != expected {
		t.Errorf("got %q, want %q", finalMessage, expected)
	}
}

// TestApplyProcessorsVariablesWithAvailableServers tests the
// @mitto:available_acp_servers variable through the full pipeline.
func TestApplyProcessorsVariablesWithAvailableServers(t *testing.T) {
	procs := []*Processor{
		{
			Name:   "server-info",
			Text:   "Available: @mitto:available_acp_servers\n\n",
			Mutate: config.ProcessorMutatePrepend,
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchFirst},
		},
	}

	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "do something",
		IsFirstMessage: true,
		AvailableACPServers: []AvailableACPServer{
			{Name: "auggie", Tags: []string{"coding"}, Current: true},
			{Name: "claude-code", Tags: []string{"fast"}},
		},
	}

	result, err := ApplyProcessors(ctx, procs, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyProcessors() error = %v", err)
	}

	finalMessage := SubstituteVariables(result.Message, input)

	// The user request is wrapped in <user_request> delimiters (first-message protection).
	expected := "Available: auggie [coding] (current), claude-code [fast]\n\n" + wrapUserRequest("do something")
	if finalMessage != expected {
		t.Errorf("got %q, want %q", finalMessage, expected)
	}
}

func TestManagerLoadAndApply(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a hook file
	hookContent := `
name: test-manager-hook
command: /bin/echo
args: ['{"message": "from manager"}']
when:
  on: userPrompt
  match: all
output: transform
`
	if err := os.WriteFile(filepath.Join(tmpDir, "hook.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write hook file: %v", err)
	}

	manager := NewManager(tmpDir, nil)

	// Test Load
	if err := manager.Load(); err != nil {
		t.Fatalf("Manager.Load() error = %v", err)
	}

	// Test Hooks
	hooks := manager.Processors()
	if len(hooks) != 1 {
		t.Fatalf("Manager.Processors() = %d hooks, want 1", len(hooks))
	}
	if hooks[0].Name != "test-manager-hook" {
		t.Errorf("Hook name = %q, want %q", hooks[0].Name, "test-manager-hook")
	}

	// Test ProcessorsDir
	if manager.ProcessorsDir() != tmpDir {
		t.Errorf("Manager.ProcessorsDir() = %q, want %q", manager.ProcessorsDir(), tmpDir)
	}

	// Test Apply
	ctx := context.Background()
	input := &ProcessorInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := manager.Apply(ctx, input)
	if err != nil {
		t.Fatalf("Manager.Apply() error = %v", err)
	}
	if result.Message != "from manager" {
		t.Errorf("Manager.Apply() = %q, want %q", result.Message, "from manager")
	}
}

func TestExecutorExecute(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "exec.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "executed"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	executor := NewExecutor(tmpDir, nil)
	hook := &Processor{
		Name:    "exec-test",
		Command: scriptPath,
		Output:  OutputTransform,
		Input:   InputNone,
		HookDir: tmpDir,
	}
	input := &ProcessorInput{
		Message:    "test",
		WorkingDir: tmpDir,
	}

	ctx := context.Background()
	output, err := executor.Execute(ctx, hook, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Message != "executed" {
		t.Errorf("Execute() message = %q, want %q", output.Message, "executed")
	}
}

func TestExecutorExecuteTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that sleeps longer than timeout
	scriptPath := filepath.Join(tmpDir, "slow.sh")
	scriptContent := `#!/bin/sh
sleep 10
echo '{"message": "should not reach"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	executor := NewExecutor(tmpDir, nil)
	hook := &Processor{
		Name:    "slow-hook",
		Command: scriptPath,
		Output:  OutputTransform,
		Timeout: Duration(100 * time.Millisecond), // Very short timeout
		HookDir: tmpDir,
	}
	input := &ProcessorInput{
		Message:    "test",
		WorkingDir: tmpDir,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, hook, input)
	if err == nil {
		t.Fatal("Execute() should error on timeout")
	}
	if !contains(err.Error(), "timed out") {
		t.Errorf("Execute() error = %q, want to contain 'timed out'", err.Error())
	}
}

func TestExecutorBuildEnvironment(t *testing.T) {
	executor := NewExecutor("/hooks", nil)
	hook := &Processor{
		FilePath: "/hooks/test.yaml",
		HookDir:  "/hooks",
		Environment: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}
	input := &ProcessorInput{
		SessionID:      "session-123",
		WorkingDir:     "/project",
		IsFirstMessage: true,
	}

	env := executor.buildEnvironment(hook, input)

	// Check for expected environment variables
	envMap := make(map[string]string)
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				envMap[e[:i]] = e[i+1:]
				break
			}
		}
	}

	if envMap["MITTO_SESSION_ID"] != "session-123" {
		t.Errorf("MITTO_SESSION_ID = %q, want %q", envMap["MITTO_SESSION_ID"], "session-123")
	}
	if envMap["MITTO_WORKING_DIR"] != "/project" {
		t.Errorf("MITTO_WORKING_DIR = %q, want %q", envMap["MITTO_WORKING_DIR"], "/project")
	}
	if envMap["MITTO_IS_FIRST_MESSAGE"] != "true" {
		t.Errorf("MITTO_IS_FIRST_MESSAGE = %q, want %q", envMap["MITTO_IS_FIRST_MESSAGE"], "true")
	}
	if envMap["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("CUSTOM_VAR = %q, want %q", envMap["CUSTOM_VAR"], "custom_value")
	}
}

func TestExecutorParseOutput(t *testing.T) {
	executor := NewExecutor("/hooks", nil)

	tests := []struct {
		name    string
		data    []byte
		wantMsg string
		wantErr bool
	}{
		{
			name:    "empty output",
			data:    []byte{},
			wantMsg: "",
			wantErr: false,
		},
		{
			name:    "valid JSON",
			data:    []byte(`{"message": "hello"}`),
			wantMsg: "hello",
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`not json`),
			wantMsg: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := executor.parseOutput(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && output.Message != tt.wantMsg {
				t.Errorf("parseOutput() message = %q, want %q", output.Message, tt.wantMsg)
			}
		})
	}
}

func TestProcessorGetMutate(t *testing.T) {
	tests := []struct {
		name     string
		mutate   config.ProcessorMutate
		expected config.ProcessorMutate
	}{
		{"empty defaults to prepend", "", config.ProcessorMutatePrepend},
		{"prepend", config.ProcessorMutatePrepend, config.ProcessorMutatePrepend},
		{"append", config.ProcessorMutateAppend, config.ProcessorMutateAppend},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Processor{Mutate: tt.mutate}
			if got := h.GetMutate(); got != tt.expected {
				t.Errorf("GetMutate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"valid duration", "10s", 10 * time.Second, false},
		{"empty defaults", "", DefaultTimeout, false},
		{"invalid", "not-a-duration", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalYAML(func(v interface{}) error {
				*(v.(*string)) = tt.input
				return nil
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && d.Duration() != tt.expected {
				t.Errorf("UnmarshalYAML() = %v, want %v", d.Duration(), tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func boolPtr(b bool) *bool {
	return &b
}

func TestCloneWithDirProcessors(t *testing.T) {
	// Create global processors directory with one processor
	globalDir := t.TempDir()
	writeYAML(t, globalDir, "global.yaml", `
name: global-proc
enabled: true
when:
  on: userPrompt
  match: all
mutate: prepend
text: "global"
`)

	// Create workspace processors directory with another processor
	wsDir := t.TempDir()
	writeYAML(t, wsDir, "workspace.yaml", `
name: ws-proc
enabled: true
when:
  on: userPrompt
  match: all
mutate: append
text: "workspace"
`)

	// Load global manager
	mgr := NewManager(globalDir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Clone with workspace processors
	cloned := mgr.CloneWithDirProcessors([]string{wsDir}, nil)

	// Should have both processors
	procs := cloned.Processors()
	if len(procs) != 2 {
		t.Fatalf("expected 2 processors, got %d", len(procs))
	}

	names := make(map[string]bool)
	for _, p := range procs {
		names[p.Name] = true
	}
	if !names["global-proc"] {
		t.Error("missing global-proc")
	}
	if !names["ws-proc"] {
		t.Error("missing ws-proc")
	}
}

func TestCloneWithDirProcessors_Override(t *testing.T) {
	// Create global processor
	globalDir := t.TempDir()
	writeYAML(t, globalDir, "shared.yaml", `
name: shared
enabled: true
when:
  on: userPrompt
  match: all
mutate: prepend
text: "global version"
`)

	// Create workspace processor with same name (should override)
	wsDir := t.TempDir()
	writeYAML(t, wsDir, "shared.yaml", `
name: shared
enabled: true
when:
  on: userPrompt
  match: all
mutate: append
text: "workspace version"
`)

	mgr := NewManager(globalDir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	cloned := mgr.CloneWithDirProcessors([]string{wsDir}, nil)
	procs := cloned.Processors()

	if len(procs) != 1 {
		t.Fatalf("expected 1 processor after override, got %d", len(procs))
	}
	if procs[0].Text != "workspace version" {
		t.Errorf("expected workspace version, got %q", procs[0].Text)
	}
	if procs[0].GetMutate() != "append" {
		t.Errorf("expected append mutate from workspace override, got %q", procs[0].GetMutate())
	}
}

func TestCloneWithDirProcessors_NonexistentDir(t *testing.T) {
	globalDir := t.TempDir()
	writeYAML(t, globalDir, "global.yaml", `
name: global-proc
enabled: true
when:
  on: userPrompt
  match: all
mutate: prepend
text: "global"
`)

	mgr := NewManager(globalDir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Non-existent directory should be silently ignored
	cloned := mgr.CloneWithDirProcessors([]string{"/nonexistent/path"}, nil)
	procs := cloned.Processors()

	if len(procs) != 1 {
		t.Fatalf("expected 1 processor, got %d", len(procs))
	}
	if procs[0].Name != "global-proc" {
		t.Errorf("expected global-proc, got %q", procs[0].Name)
	}
}

func TestCloneWithDirProcessors_Priority(t *testing.T) {
	globalDir := t.TempDir()
	writeYAML(t, globalDir, "high.yaml", `
name: high-priority
enabled: true
when:
  on: userPrompt
  match: all
priority: 50
mutate: prepend
text: "high"
`)

	wsDir := t.TempDir()
	writeYAML(t, wsDir, "low.yaml", `
name: low-priority
enabled: true
when:
  on: userPrompt
  match: all
priority: 10
mutate: prepend
text: "low"
`)

	mgr := NewManager(globalDir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	cloned := mgr.CloneWithDirProcessors([]string{wsDir}, nil)
	procs := cloned.Processors()

	if len(procs) != 2 {
		t.Fatalf("expected 2 processors, got %d", len(procs))
	}

	// Should be sorted by priority: low(10) before high(50)
	if procs[0].Name != "low-priority" {
		t.Errorf("expected low-priority first, got %q", procs[0].Name)
	}
	if procs[1].Name != "high-priority" {
		t.Errorf("expected high-priority second, got %q", procs[1].Name)
	}
}

func TestCloneWithDirProcessors_EmptyDirs(t *testing.T) {
	globalDir := t.TempDir()
	writeYAML(t, globalDir, "global.yaml", `
name: global
enabled: true
when:
  on: userPrompt
  match: all
mutate: prepend
text: "global"
`)

	mgr := NewManager(globalDir, nil)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Empty dirs list should return the same manager
	result := mgr.CloneWithDirProcessors([]string{}, nil)
	if result != mgr {
		t.Error("expected same manager for empty dirs")
	}
}

func TestProcessorIsPromptMode(t *testing.T) {
	tests := []struct {
		name     string
		proc     *Processor
		expected bool
	}{
		{
			name:     "prompt set, command and text empty → true",
			proc:     &Processor{Prompt: "analyze this"},
			expected: true,
		},
		{
			name:     "prompt and command set → false",
			proc:     &Processor{Prompt: "analyze this", Command: "/bin/echo"},
			expected: false,
		},
		{
			name:     "prompt and text set → false",
			proc:     &Processor{Prompt: "analyze this", Text: "some text"},
			expected: false,
		},
		{
			name:     "all empty → false",
			proc:     &Processor{},
			expected: false,
		},
		{
			name:     "only command set → false",
			proc:     &Processor{Command: "/bin/echo"},
			expected: false,
		},
		{
			name:     "only text set → false",
			proc:     &Processor{Text: "some text"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.proc.IsPromptMode(); got != tt.expected {
				t.Errorf("IsPromptMode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLoaderPromptModeValidation(t *testing.T) {
	t.Run("valid prompt-mode processor", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "valid.yaml", `
name: valid-prompt
when:
  on: userPrompt
  match: all
prompt: "Use mitto_conversation_history to retrieve messages."
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(procs) != 1 {
			t.Fatalf("expected 1 processor, got %d", len(procs))
		}
		if !procs[0].IsPromptMode() {
			t.Error("expected IsPromptMode() = true")
		}
	})

	t.Run("prompt + command → validation error (file skipped)", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "bad.yaml", `
name: bad-proc
when:
  on: userPrompt
  match: all
prompt: "Analyze this"
command: /bin/echo
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() should not error (bad files are skipped): %v", err)
		}
		if len(procs) != 0 {
			t.Errorf("expected 0 processors (bad file skipped), got %d", len(procs))
		}
	})

	t.Run("prompt + text → validation error (file skipped)", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "bad.yaml", `
name: bad-proc
when:
  on: userPrompt
  match: all
prompt: "Analyze this"
text: "some text"
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() should not error: %v", err)
		}
		if len(procs) != 0 {
			t.Errorf("expected 0 processors (bad file skipped), got %d", len(procs))
		}
	})
}

func TestManagerRoutesPromptModeToApplyWithRerun(t *testing.T) {
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{
		{
			Name:   "test-prompt",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Analyze the session @mitto:session_id",
		},
	}

	called := make(chan string, 1)
	mgr.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
		called <- prompt
		return nil
	})

	input := &ProcessorInput{
		Message:        "hello",
		IsFirstMessage: true,
		WorkspaceUUID:  "ws-123",
		SessionID:      "sess-abc",
	}

	result, err := mgr.Apply(context.Background(), input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	// Prompt-mode doesn't transform the message, but first-message wrapping applies.
	if result.Message != wrapUserRequest("hello") {
		t.Errorf("expected first-message-wrapped message, got %q", result.Message)
	}

	// Wait for async dispatch
	select {
	case prompt := <-called:
		if !strings.Contains(prompt, "sess-abc") {
			t.Errorf("expected prompt to contain substituted session_id, got %q", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PromptFunc was not called within timeout")
	}
}

func TestPromptModeSingleNotBatched(t *testing.T) {
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{
		{
			Name:   "solo-proc",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Solo prompt",
		},
	}

	type call struct {
		name   string
		prompt string
	}
	calls := make(chan call, 5)
	mgr.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
		calls <- call{name: procName, prompt: prompt}
		return nil
	})

	input := &ProcessorInput{
		Message:        "hello",
		IsFirstMessage: true,
		WorkspaceUUID:  "ws-single",
	}

	_, err := mgr.Apply(context.Background(), input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	select {
	case c := <-calls:
		// Single processor: dispatched with its own name, no batch header.
		if c.name != "solo-proc" {
			t.Errorf("expected name %q, got %q", "solo-proc", c.name)
		}
		if strings.Contains(c.prompt, "We would like to fulfill") {
			t.Errorf("single processor should not use batch header, got prompt %q", c.prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PromptFunc was not called within timeout")
	}

	// Ensure only one call was made.
	select {
	case c := <-calls:
		t.Errorf("expected exactly 1 promptFunc call, got a second: name=%q", c.name)
	default:
		// Good — no extra calls.
	}
}

func TestPromptModeBatching(t *testing.T) {
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{
		{
			Name:   "proc-alpha",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Alpha task prompt",
		},
		{
			Name:   "proc-beta",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Beta task prompt",
		},
	}

	type call struct {
		name   string
		prompt string
	}
	calls := make(chan call, 5)
	mgr.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
		calls <- call{name: procName, prompt: prompt}
		return nil
	})

	input := &ProcessorInput{
		Message:        "hello",
		IsFirstMessage: true,
		WorkspaceUUID:  "ws-batch",
	}

	_, err := mgr.Apply(context.Background(), input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	select {
	case c := <-calls:
		// Should be a single batched call, not two separate calls.
		if !strings.Contains(c.name, "proc-alpha") {
			t.Errorf("combined name should contain proc-alpha, got %q", c.name)
		}
		if !strings.Contains(c.name, "proc-beta") {
			t.Errorf("combined name should contain proc-beta, got %q", c.name)
		}
		if !strings.Contains(c.prompt, "We would like to fulfill the following requirements:") {
			t.Errorf("batched prompt should have header, got %q", c.prompt)
		}
		if !strings.Contains(c.prompt, "Alpha task prompt") {
			t.Errorf("batched prompt should contain alpha prompt, got %q", c.prompt)
		}
		if !strings.Contains(c.prompt, "Beta task prompt") {
			t.Errorf("batched prompt should contain beta prompt, got %q", c.prompt)
		}
		if !strings.Contains(c.prompt, "## Requirement 1: proc-alpha") {
			t.Errorf("batched prompt should have requirement header for proc-alpha, got %q", c.prompt)
		}
		if !strings.Contains(c.prompt, "## Requirement 2: proc-beta") {
			t.Errorf("batched prompt should have requirement header for proc-beta, got %q", c.prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PromptFunc was not called within timeout")
	}

	// Ensure only ONE call was made (not two separate calls).
	select {
	case c := <-calls:
		t.Errorf("expected exactly 1 batched promptFunc call, got a second: name=%q", c.name)
	default:
		// Good — only one combined call.
	}
}

func writeYAML(t *testing.T, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// TestLoaderMultiDoc_TwoValidProcessors verifies that a single YAML file with
// two `---`-separated valid processor documents loads both processors.
func TestLoaderMultiDoc_TwoValidProcessors(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "multi.yaml", `
name: proc-one
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: proc-two
when:
  on: agentResponded
  match: all
command: /bin/echo
`)

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("expected 2 processors, got %d", len(procs))
	}
	names := map[string]bool{}
	for _, p := range procs {
		names[p.Name] = true
	}
	if !names["proc-one"] {
		t.Error("missing proc-one")
	}
	if !names["proc-two"] {
		t.Error("missing proc-two")
	}
}

// TestLoaderMultiDoc_ValidPlusInvalid verifies that a file containing one valid
// and one invalid document loads only the valid one and does not error.
func TestLoaderMultiDoc_ValidPlusInvalid(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "mixed.yaml", `
name: good-proc
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: bad-proc
when:
  on: userPrompt
  match: all
# missing command/text/prompt — should be skipped
`)

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() should not error (bad docs are skipped), got: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 processor, got %d", len(procs))
	}
	if procs[0].Name != "good-proc" {
		t.Errorf("expected good-proc, got %q", procs[0].Name)
	}
}

// TestLoaderMultiDoc_EmptyDocumentsSkipped verifies that empty or comment-only
// documents between `---` separators are silently skipped.
func TestLoaderMultiDoc_EmptyDocumentsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "gaps.yaml", `
---
# just a comment — no fields
---
name: real-proc
when:
  on: userPrompt
  match: all
command: /bin/echo
---
`)

	loader := NewLoader(dir, nil)
	procs, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 processor, got %d", len(procs))
	}
	if procs[0].Name != "real-proc" {
		t.Errorf("expected real-proc, got %q", procs[0].Name)
	}
}

// TestLoaderMultiDoc_LoadFileAll_ReturnsAll verifies that LoadFileAll returns
// all valid documents from a multi-document file.
func TestLoaderMultiDoc_LoadFileAll_ReturnsAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "two.yaml")
	content := `name: alpha
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: beta
when:
  on: userPrompt
  match: first
command: /bin/echo
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loader := NewLoader(dir, nil)
	procs, err := loader.LoadFileAll(path)
	if err != nil {
		t.Fatalf("LoadFileAll() error = %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("expected 2, got %d", len(procs))
	}
	if procs[0].Name != "alpha" || procs[1].Name != "beta" {
		t.Errorf("unexpected names: %q, %q", procs[0].Name, procs[1].Name)
	}
}

// TestLoaderMultiDoc_LoadFile_ReturnsFirst verifies backward-compatible
// LoadFile behaviour: only the first document is returned.
func TestLoaderMultiDoc_LoadFile_ReturnsFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "two.yaml")
	content := `name: first-doc
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: second-doc
when:
  on: userPrompt
  match: all
command: /bin/echo
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loader := NewLoader(dir, nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if proc == nil {
		t.Fatal("LoadFile() returned nil, want a processor")
	}
	if proc.Name != "first-doc" {
		t.Errorf("LoadFile() name = %q, want %q", proc.Name, "first-doc")
	}
}

// TestLoaderMultiDoc_FilePath verifies that FilePath and HookDir are set on
// every processor loaded from a multi-document file.
func TestLoaderMultiDoc_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "two.yaml")
	content := `name: p1
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: p2
when:
  on: userPrompt
  match: all
command: /bin/echo
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loader := NewLoader(dir, nil)
	procs, err := loader.LoadFileAll(path)
	if err != nil {
		t.Fatalf("LoadFileAll() error = %v", err)
	}
	for _, p := range procs {
		if p.FilePath != path {
			t.Errorf("processor %q FilePath = %q, want %q", p.Name, p.FilePath, path)
		}
		if p.HookDir != dir {
			t.Errorf("processor %q HookDir = %q, want %q", p.Name, p.HookDir, dir)
		}
	}
}

// TestIsMultiDocFile verifies the IsMultiDocFile helper.
func TestIsMultiDocFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
		wantErr bool
	}{
		{
			name: "single doc → false",
			content: `name: proc-a
when:
  on: userPrompt
  match: all
command: /bin/echo
`,
			want: false,
		},
		{
			name: "two docs → true",
			content: `name: proc-a
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: proc-b
when:
  on: agentResponded
  match: all
command: /bin/echo
`,
			want: true,
		},
		{
			name: "empty/comment-only docs only → false",
			content: `---
# just a comment
---
`,
			want: false,
		},
		{
			name: "empty doc then one real doc → false",
			content: `---
# comment
---
name: proc-a
when:
  on: userPrompt
  match: all
command: /bin/echo
`,
			want: false,
		},
		{
			name:    "YAML syntax error → error",
			content: "invalid: yaml: content:",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "*.yaml")
			if err != nil {
				t.Fatalf("CreateTemp: %v", err)
			}
			if _, err := f.WriteString(tt.content); err != nil {
				t.Fatalf("WriteString: %v", err)
			}
			f.Close()

			got, err := IsMultiDocFile(f.Name())
			if (err != nil) != tt.wantErr {
				t.Errorf("IsMultiDocFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("IsMultiDocFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUpdateProcessorFileEnabled_SingleDoc verifies that a single-document file
// is updated in-place (existing behavior).
func TestUpdateProcessorFileEnabled_SingleDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.yaml")
	original := `name: my-proc
enabled: true
when:
  on: userPrompt
  match: all
command: /bin/echo
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := UpdateProcessorFileEnabled(path, false); err != nil {
		t.Fatalf("UpdateProcessorFileEnabled() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("expected 'enabled: false' in file, got:\n%s", string(data))
	}
}

// TestUpdateProcessorFileEnabled_MultiDoc verifies that calling
// UpdateProcessorFileEnabled on a multi-document file returns an error and
// does NOT modify the file.
func TestUpdateProcessorFileEnabled_MultiDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.yaml")
	original := `name: proc-a
when:
  on: userPrompt
  match: all
command: /bin/echo
---
name: proc-b
when:
  on: agentResponded
  match: all
command: /bin/echo
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := UpdateProcessorFileEnabled(path, false)
	if err == nil {
		t.Fatal("UpdateProcessorFileEnabled() should return error for multi-doc file, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to edit multi-document") {
		t.Errorf("error message should mention 'refusing to edit multi-document', got: %v", err)
	}

	// File must be byte-identical to original.
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile after error: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("file was modified despite error:\ngot:\n%s\nwant:\n%s", string(data), original)
	}
}

// makeAfterInput returns a minimal AfterProcessorInput for tests.
// SessionDir is set to a stable key so the MemoryStateStore injected by
// makeAfterManager shares state across successive calls within the same test.
func makeAfterInput(origin, stopReason string) AfterProcessorInput {
	return AfterProcessorInput{
		SessionID:     "test-session",
		SessionDir:    "test-session-dir", // key for MemoryStateStore
		WorkspaceUUID: "test-workspace",
		Origin:        origin,
		StopReason:    stopReason,
		UserPrompt:    "hello",
		AgentMessages: []string{"world"},
		StartedAt:     time.Now().Add(-time.Second),
		EndedAt:       time.Now(),
	}
}

// makeAfterManager returns a Manager with no processors directory, using the
// given processors slice directly (bypasses the filesystem loader).
// A MemoryStateStore is injected so match:first / cadence state is preserved
// across successive ApplyAfter calls within the same test.
func makeAfterManager(procs []*Processor) *Manager {
	m := NewManager("", nil)
	m.processors = procs
	m.SetStateStore(NewMemoryStateStore())
	return m
}

// TestApplyAfter_EmptyPipeline verifies that an empty processor list returns
// a zero-value ApplyAfterResult without panicking.
func TestApplyAfter_EmptyPipeline(t *testing.T) {
	m := makeAfterManager(nil)
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))
	if len(result.Notifications) != 0 || len(result.ActionButtons) != 0 ||
		len(result.UserDataPatch) != 0 || len(result.Errors) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

// TestApplyAfter_SkipsUserPromptProcessors ensures userPrompt processors are ignored.
func TestApplyAfter_SkipsUserPromptProcessors(t *testing.T) {
	proc := &Processor{
		Name:    "pre-phase",
		When:    WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		Command: "echo",
		Output:  OutputDiscard,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
}

// TestApplyAfter_StopReasonFilter verifies that processors skip when the stop
// reason does not match the processor's stopReasons list.
func TestApplyAfter_StopReasonFilter(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "echo.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\necho done"), 0755)

	proc := &Processor{
		Name:    "end-turn-only",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputDiscard,
	}
	m := makeAfterManager([]*Processor{proc})

	// Should fire for endTurn
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors for endTurn: %v", result.Errors)
	}

	// Should skip for maxTokens
	result2 := m.ApplyAfter(context.Background(), makeAfterInput("user", "maxTokens"))
	if len(result2.Errors) != 0 {
		t.Errorf("unexpected errors for maxTokens: %v", result2.Errors)
	}
}

// TestApplyAfter_OriginFilter verifies that excludeOrigins causes the processor
// to be skipped when the origin matches.
func TestApplyAfter_OriginFilter(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "echo.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\necho done"), 0755)

	proc := &Processor{
		Name:    "no-periodic",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}, ExcludeOrigins: []string{"periodic-runner"}},
		Command: scriptPath,
		Output:  OutputDiscard,
	}
	m := makeAfterManager([]*Processor{proc})

	// Should skip for periodic-runner
	result := m.ApplyAfter(context.Background(), makeAfterInput("periodic-runner", "end_turn"))
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors for excluded origin, got %v", result.Errors)
	}

	// Should fire for user
	result2 := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))
	if len(result2.Errors) != 0 {
		t.Errorf("expected no errors for user origin, got %v", result2.Errors)
	}
}

// TestApplyAfter_MatchFirst verifies that match:first fires exactly once.
func TestApplyAfter_MatchFirst(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '{\"title\":\"hello\",\"message\":\"world\"}'"), 0755)

	proc := &Processor{
		Name:    "first-only",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchFirst, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	input := makeAfterInput("user", "end_turn")

	// First call: fires
	r1 := m.ApplyAfter(context.Background(), input)
	if len(r1.Notifications) != 1 {
		t.Fatalf("first call: expected 1 notification, got %d", len(r1.Notifications))
	}

	// Second call: must be skipped
	r2 := m.ApplyAfter(context.Background(), input)
	if len(r2.Notifications) != 0 {
		t.Errorf("second call: expected 0 notifications (match=first), got %d", len(r2.Notifications))
	}
}

// TestApplyAfter_MatchAllExceptFirst verifies that match:allExceptFirst skips the
// first call and fires from the second onward.
func TestApplyAfter_MatchAllExceptFirst(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '{\"title\":\"T\",\"message\":\"M\"}'"), 0755)

	proc := &Processor{
		Name:    "not-first",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAllExceptFirst, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	input := makeAfterInput("user", "end_turn")

	// First call: skipped
	r1 := m.ApplyAfter(context.Background(), input)
	if len(r1.Notifications) != 0 {
		t.Errorf("first call: expected 0 notifications, got %d", len(r1.Notifications))
	}

	// Second call: fires
	r2 := m.ApplyAfter(context.Background(), input)
	if len(r2.Notifications) != 1 {
		t.Errorf("second call: expected 1 notification, got %d", len(r2.Notifications))
	}
}

// TestApplyAfter_OutputDiscard verifies that output:discard runs the command but
// produces no entries in the result.
func TestApplyAfter_OutputDiscard(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "side_effect.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'should be discarded'"), 0755)

	proc := &Processor{
		Name:    "side-effect",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputDiscard,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Notifications) != 0 || len(result.ActionButtons) != 0 ||
		len(result.UserDataPatch) != 0 || len(result.Errors) != 0 {
		t.Errorf("expected empty result for discard, got %+v", result)
	}
}

// TestApplyAfter_OutputNotify_JSON verifies JSON-form notify output is parsed correctly.
func TestApplyAfter_OutputNotify_JSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"Alert","message":"Done","style":"success"}'`), 0755)

	proc := &Processor{
		Name:    "json-notify",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(result.Notifications))
	}
	n := result.Notifications[0]
	if n.Title != "Alert" || n.Message != "Done" || n.Style != "success" {
		t.Errorf("unexpected notification: %+v", n)
	}
}

// TestApplyAfter_OutputNotify_PlainText verifies plain-text notify output.
func TestApplyAfter_OutputNotify_PlainText(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'Title Line\nBody text here'"), 0755)

	proc := &Processor{
		Name:    "text-notify",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(result.Notifications))
	}
	n := result.Notifications[0]
	if n.Title != "Title Line" {
		t.Errorf("expected title 'Title Line', got %q", n.Title)
	}
	if n.Style != "info" {
		t.Errorf("expected style 'info', got %q", n.Style)
	}
}

// TestApplyAfter_OutputActionButtons_Array verifies JSON array form.
func TestApplyAfter_OutputActionButtons_Array(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "buttons.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '[{"label":"Run tests","prompt":"Run tests now"},{"label":"Deploy","prompt":"Deploy to prod"}]'`), 0755)

	proc := &Processor{
		Name:    "array-buttons",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputActionButtons,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.ActionButtons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(result.ActionButtons))
	}
	if result.ActionButtons[0].Label != "Run tests" {
		t.Errorf("expected label 'Run tests', got %q", result.ActionButtons[0].Label)
	}
}

// TestApplyAfter_OutputActionButtons_SingleObject verifies single-object form.
func TestApplyAfter_OutputActionButtons_SingleObject(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "button.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"label":"Deploy","prompt":"Deploy now"}'`), 0755)

	proc := &Processor{
		Name:    "single-button",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputActionButtons,
	}
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.ActionButtons) != 1 {
		t.Fatalf("expected 1 button, got %d", len(result.ActionButtons))
	}
	if result.ActionButtons[0].Label != "Deploy" {
		t.Errorf("expected label 'Deploy', got %q", result.ActionButtons[0].Label)
	}
}

// TestApplyAfter_OutputUserData verifies that multiple processors' patches are merged
// and that later processors win on key collision.
func TestApplyAfter_OutputUserData_Merge(t *testing.T) {
	dir := t.TempDir()
	script1 := filepath.Join(dir, "ud1.sh")
	script2 := filepath.Join(dir, "ud2.sh")
	os.WriteFile(script1, []byte(`#!/bin/sh
printf '{"lang":"go","version":"1.0"}'`), 0755)
	os.WriteFile(script2, []byte(`#!/bin/sh
printf '{"version":"2.0","extra":"yes"}'`), 0755)

	proc1 := &Processor{
		Name:    "ud1",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: script1,
		Output:  OutputUserData,
	}
	proc2 := &Processor{
		Name:    "ud2",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: script2,
		Output:  OutputUserData,
	}
	m := makeAfterManager([]*Processor{proc1, proc2})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.UserDataPatch["lang"] != "go" {
		t.Errorf("expected lang=go, got %q", result.UserDataPatch["lang"])
	}
	// proc2 overrides version
	if result.UserDataPatch["version"] != "2.0" {
		t.Errorf("expected version=2.0 (proc2 wins), got %q", result.UserDataPatch["version"])
	}
	if result.UserDataPatch["extra"] != "yes" {
		t.Errorf("expected extra=yes, got %q", result.UserDataPatch["extra"])
	}
}

// TestApplyAfter_CommandFailure verifies that a failing command records a non-fatal
// error and that later processors still run.
func TestApplyAfter_CommandFailure(t *testing.T) {
	dir := t.TempDir()
	failScript := filepath.Join(dir, "fail.sh")
	succScript := filepath.Join(dir, "succ.sh")
	os.WriteFile(failScript, []byte("#!/bin/sh\nexit 1"), 0755)
	os.WriteFile(succScript, []byte(`#!/bin/sh
printf '{"title":"OK","message":"ran"}'`), 0755)

	proc1 := &Processor{
		Name:    "will-fail",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: failScript,
		Output:  OutputDiscard,
	}
	proc2 := &Processor{
		Name:    "will-succeed",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: succScript,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc1, proc2})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error (from will-fail), got %d: %v", len(result.Errors), result.Errors)
	}
	if result.Errors[0].ProcessorName != "will-fail" {
		t.Errorf("expected error from 'will-fail', got %q", result.Errors[0].ProcessorName)
	}
	if len(result.Notifications) != 1 {
		t.Errorf("expected 1 notification from will-succeed, got %d", len(result.Notifications))
	}
}

// TestApplyAfter_StdinPayload verifies that the JSON stdin payload contains expected fields.
func TestApplyAfter_StdinPayload(t *testing.T) {
	dir := t.TempDir()
	// Read stdin and emit it as-is so we can inspect it via parseNotifyOutput hack
	captureScript := filepath.Join(dir, "capture.sh")
	outFile := filepath.Join(dir, "stdin.json")
	script := fmt.Sprintf("#!/bin/sh\ncat > %s\nprintf '{\"title\":\"ok\",\"message\":\"done\"}'", outFile)
	os.WriteFile(captureScript, []byte(script), 0755)

	proc := &Processor{
		Name:    "capture",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: captureScript,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})

	input := AfterProcessorInput{
		SessionID:     "sess-abc",
		Origin:        "user",
		StopReason:    "end_turn",
		UserPrompt:    "hello world",
		AgentMessages: []string{"response text"},
		StartedAt:     time.Now().Add(-time.Second),
		EndedAt:       time.Now(),
	}
	result := m.ApplyAfter(context.Background(), input)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read captured stdin: %v", err)
	}

	if !strings.Contains(string(data), `"sessionId":"sess-abc"`) {
		t.Errorf("stdin JSON missing sessionId, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"origin":"user"`) {
		t.Errorf("stdin JSON missing origin, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"stopReason":"end_turn"`) {
		t.Errorf("stdin JSON missing stopReason, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"userPrompt":"hello world"`) {
		t.Errorf("stdin JSON missing userPrompt, got: %s", string(data))
	}
}

// TestParseNotifyOutput tests the notify output parser in isolation.
func TestParseNotifyOutput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLen   int
		wantTitle string
		wantStyle string
	}{
		{"empty", "", 0, "", ""},
		{"json full", `{"title":"T","message":"M","style":"warning"}`, 1, "T", "warning"},
		{"json default style", `{"title":"T","message":"M"}`, 1, "T", "info"},
		{"plain text single line", "Hello there", 1, "Hello there", "info"},
		{"plain text multi line", "Title\nBody text", 1, "Title", "info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNotifyOutput(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("expected %d notifications, got %d", tt.wantLen, len(got))
			}
			if tt.wantLen > 0 {
				if got[0].Title != tt.wantTitle {
					t.Errorf("title: got %q, want %q", got[0].Title, tt.wantTitle)
				}
				if got[0].Style != tt.wantStyle {
					t.Errorf("style: got %q, want %q", got[0].Style, tt.wantStyle)
				}
			}
		})
	}
}

// TestParseActionButtonsOutput tests the actionButtons output parser in isolation.
func TestParseActionButtonsOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{"empty", "", 0, false},
		{"array", `[{"label":"A","prompt":"a"},{"label":"B","prompt":"b"}]`, 2, false},
		{"single object", `{"label":"X","prompt":"x"}`, 1, false},
		{"invalid json", `not json`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseActionButtonsOutput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && len(got) != tt.wantLen {
				t.Errorf("expected %d buttons, got %d", tt.wantLen, len(got))
			}
		})
	}
}

// TestParseUserDataOutput tests the userData output parser in isolation.
func TestParseUserDataOutput(t *testing.T) {
	got, err := parseUserDataOutput(`{"key":"val","other":"42"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "val" || got["other"] != "42" {
		t.Errorf("unexpected patch: %v", got)
	}

	_, err = parseUserDataOutput(`not json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}

	got2, err := parseUserDataOutput("")
	if err != nil || len(got2) != 0 {
		t.Errorf("expected nil result for empty input, got %v %v", got2, err)
	}
}

// TestApplyAfter_PromptMode verifies that a prompt-mode processor renders
// its template with after-phase variables and parses the result as notify output.
// TestApplyAfter_PromptMode_Dispatched verifies that prompt-mode processors in the
// agentResponded phase are dispatched via promptFunc (fire-and-forget) rather than
// treated as command output. The output: field is ignored for prompt-mode processors.
func TestApplyAfter_PromptMode_Dispatched(t *testing.T) {
	var dispatched []struct{ workspace, name, prompt string }
	var mu sync.Mutex

	proc := &Processor{
		Name:   "auggie-update-rules",
		When:   WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Prompt: `Update rules for stop: @mitto:stop_reason origin: @mitto:origin`,
		// Output field is intentionally set to OutputNotify to confirm it is ignored for prompt-mode.
		Output: OutputNotify,
	}

	m := makeAfterManager([]*Processor{proc})
	m.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, struct{ workspace, name, prompt string }{
			workspace: workspaceUUID,
			name:      processorName,
			prompt:    prompt,
		})
		return nil
	})

	input := makeAfterInput("user", "end_turn")
	result := m.ApplyAfter(context.Background(), input)

	// Wait briefly for the async goroutine to finish.
	time.Sleep(50 * time.Millisecond)

	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	// Prompt-mode processors must NOT produce notifications — they are dispatched.
	if len(result.Notifications) != 0 {
		t.Errorf("expected 0 notifications for prompt-mode processor, got %d", len(result.Notifications))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatched prompt, got %d", len(dispatched))
	}
	d := dispatched[0]
	if d.workspace != "test-workspace" {
		t.Errorf("expected workspace 'test-workspace', got %q", d.workspace)
	}
	if d.name != "auggie-update-rules" {
		t.Errorf("expected processor name 'auggie-update-rules', got %q", d.name)
	}
	wantPrompt := "Update rules for stop: end_turn origin: user"
	if d.prompt != wantPrompt {
		t.Errorf("expected prompt %q, got %q", wantPrompt, d.prompt)
	}
}

// TestPromptMode_ArgSubstitution_BeforePhase tests ${VAR} / ${VAR:-inline} substitution
// in prompt-mode before-phase (userPrompt) processors (mitto-5g2v.2).
func TestPromptMode_ArgSubstitution_BeforePhase(t *testing.T) {
	proc := &Processor{
		Name:   "save-rules",
		When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		Prompt: "Save to ${filename} using mode ${mode}.",
		Parameters: []config.PromptParameter{
			{Name: "filename", Type: "text", Default: "AGENTS.md"},
			{Name: "mode", Type: "text", Default: "append"},
		},
	}

	called := make(chan string, 1)
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{proc}
	mgr.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
		called <- prompt
		return nil
	})

	t.Run("defaults used when no override", func(t *testing.T) {
		input := &ProcessorInput{
			Message:       "hello",
			WorkspaceUUID: "ws-1",
		}
		_, err := mgr.Apply(context.Background(), input)
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		select {
		case got := <-called:
			want := "Save to AGENTS.md using mode append."
			if got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("PromptFunc was not called within timeout")
		}
	})

	t.Run("workspace override wins over default", func(t *testing.T) {
		input := &ProcessorInput{
			Message:       "hello",
			WorkspaceUUID: "ws-1",
			ProcessorArgOverrides: map[string]map[string]string{
				"save-rules": {"filename": "CLAUDE.md"},
			},
		}
		_, err := mgr.Apply(context.Background(), input)
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		select {
		case got := <-called:
			want := "Save to CLAUDE.md using mode append."
			if got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("PromptFunc was not called within timeout")
		}
	})

	t.Run("inline default in body works when no declared param", func(t *testing.T) {
		// A processor with no declared parameters but using ${VAR:-inline} in the body.
		proc2 := &Processor{
			Name:   "inline-default",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Use ${tool:-bash} for this.",
		}
		mgr2 := NewManager("", nil)
		mgr2.processors = []*Processor{proc2}
		called2 := make(chan string, 1)
		mgr2.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
			called2 <- prompt
			return nil
		})

		input := &ProcessorInput{Message: "hi", WorkspaceUUID: "ws-x"}
		if _, err := mgr2.Apply(context.Background(), input); err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		select {
		case got := <-called2:
			want := "Use bash for this."
			if got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("PromptFunc was not called within timeout")
		}
	})

	t.Run("escaped placeholder is preserved", func(t *testing.T) {
		proc3 := &Processor{
			Name:   "escape-test",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: `Literal \${filename} not substituted.`,
		}
		mgr3 := NewManager("", nil)
		mgr3.processors = []*Processor{proc3}
		called3 := make(chan string, 1)
		mgr3.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
			called3 <- prompt
			return nil
		})

		input := &ProcessorInput{Message: "hi", WorkspaceUUID: "ws-x"}
		if _, err := mgr3.Apply(context.Background(), input); err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		select {
		case got := <-called3:
			want := "Literal ${filename} not substituted."
			if got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("PromptFunc was not called within timeout")
		}
	})
}

// TestPromptMode_ArgSubstitution_AfterPhase tests ${VAR} substitution in prompt-mode
// after-phase (agentResponded) processors (mitto-5g2v.2).
func TestPromptMode_ArgSubstitution_AfterPhase(t *testing.T) {
	proc := &Processor{
		Name: "report-to-file",
		When: WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Prompt: "Write summary to ${dest}.",
		Parameters: []config.PromptParameter{
			{Name: "dest", Type: "text", Default: "SUMMARY.md"},
		},
	}

	t.Run("default used when no override", func(t *testing.T) {
		var mu sync.Mutex
		var dispatched []string
		m := makeAfterManager([]*Processor{proc})
		m.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
			mu.Lock()
			dispatched = append(dispatched, prompt)
			mu.Unlock()
			return nil
		})

		m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()
		if len(dispatched) != 1 {
			t.Fatalf("expected 1 dispatched prompt, got %d", len(dispatched))
		}
		want := "Write summary to SUMMARY.md."
		if dispatched[0] != want {
			t.Errorf("prompt = %q, want %q", dispatched[0], want)
		}
	})

	t.Run("workspace override wins over default", func(t *testing.T) {
		var mu sync.Mutex
		var dispatched []string
		m := makeAfterManager([]*Processor{proc})
		m.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
			mu.Lock()
			dispatched = append(dispatched, prompt)
			mu.Unlock()
			return nil
		})

		input := makeAfterInput("user", "end_turn")
		input.ProcessorArgOverrides = map[string]map[string]string{
			"report-to-file": {"dest": "NOTES.md"},
		}
		m.ApplyAfter(context.Background(), input)
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()
		if len(dispatched) != 1 {
			t.Fatalf("expected 1 dispatched prompt, got %d", len(dispatched))
		}
		want := "Write summary to NOTES.md."
		if dispatched[0] != want {
			t.Errorf("prompt = %q, want %q", dispatched[0], want)
		}
	})
}

// TestPromptMode_ArgSubstitution_MittoRCPersistence is an integration test that
// exercises the full persistence → resolution → substitution → dispatch chain:
//   1. Write a per-workspace override to a real .mittorc via SaveWorkspaceRCProcessorArguments.
//   2. Read it back via LoadWorkspaceRC and build the ProcessorArgOverrides map.
//   3. Apply a prompt-mode processor whose body uses ${HistoryLimit:-10}.
//   4. Assert the dispatched prompt reflects the override (25) and the default (10).
func TestPromptMode_ArgSubstitution_MittoRCPersistence(t *testing.T) {
	dir := t.TempDir()
	procName := "auggie-update-rules-test"

	// Step 1: persist override via the real RC writer.
	if err := config.SaveWorkspaceRCProcessorArguments(dir, procName, map[string]string{"HistoryLimit": "25"}); err != nil {
		t.Fatalf("SaveWorkspaceRCProcessorArguments: %v", err)
	}

	// Step 2: read back via LoadWorkspaceRC (mirrors session_manager.go's GetWorkspaceProcessorOverrides).
	rc, err := config.LoadWorkspaceRC(dir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC: %v", err)
	}
	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil after writing override")
	}
	argOverrides := make(map[string]map[string]string)
	for _, o := range rc.ProcessorOverrides {
		if len(o.Arguments) > 0 {
			argOverrides[o.Name] = o.Arguments
		}
	}
	if argOverrides[procName]["HistoryLimit"] != "25" {
		t.Fatalf("expected HistoryLimit=25 in loaded overrides, got %v", argOverrides[procName])
	}

	// Step 3: build a prompt-mode processor with the Parameters block.
	proc := &Processor{
		Name: procName,
		When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		Prompt: "Review last_n: ${HistoryLimit:-10} messages.",
		Parameters: []config.PromptParameter{
			{Name: "HistoryLimit", Type: "text", Default: "10"},
		},
	}
	mgr := NewManager("", nil)
	mgr.processors = []*Processor{proc}

	// Step 4a: override from .mittorc wins → dispatched prompt should use 25.
	called := make(chan string, 1)
	mgr.SetPromptFunc(func(_ context.Context, _, _, prompt string) error {
		called <- prompt
		return nil
	})
	_, err = mgr.Apply(context.Background(), &ProcessorInput{
		Message:               "hello",
		WorkspaceUUID:         "ws-1",
		ProcessorArgOverrides: argOverrides,
	})
	if err != nil {
		t.Fatalf("Apply (with override): %v", err)
	}
	select {
	case got := <-called:
		want := "Review last_n: 25 messages."
		if got != want {
			t.Errorf("with override: prompt = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PromptFunc not called within timeout (with override)")
	}

	// Step 4b: no override → declared default (10) is used.
	called2 := make(chan string, 1)
	mgr.SetPromptFunc(func(_ context.Context, _, _, prompt string) error {
		called2 <- prompt
		return nil
	})
	_, err = mgr.Apply(context.Background(), &ProcessorInput{
		Message:       "hello",
		WorkspaceUUID: "ws-1",
		// No ProcessorArgOverrides — should fall back to declared default.
	})
	if err != nil {
		t.Fatalf("Apply (no override): %v", err)
	}
	select {
	case got := <-called2:
		want := "Review last_n: 10 messages."
		if got != want {
			t.Errorf("no override: prompt = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PromptFunc not called within timeout (no override)")
	}
}

// TestApplyAfter_PromptMode_NoPromptFunc verifies that prompt-mode processors are
// skipped gracefully (not counted as errors) when no PromptFunc is configured.
func TestApplyAfter_PromptMode_NoPromptFunc(t *testing.T) {
	proc := &Processor{
		Name:   "orphan-prompt",
		When:   WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Prompt: `Do something`,
	}
	// makeAfterManager does not set a PromptFunc.
	m := makeAfterManager([]*Processor{proc})
	result := m.ApplyAfter(context.Background(), makeAfterInput("user", "end_turn"))

	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors (skipped gracefully), got %v", result.Errors)
	}
	if len(result.Notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(result.Notifications))
	}
}

// TestApplyAfter_Cadence_EveryNTurns verifies that a processor with
// cadence.everyNTurns:2 fires on every 2nd turn (turns 2, 4, 6, ...).
// Pre-increment semantics: TurnsSinceLastFire is incremented before the gate
// check, so everyNTurns:2 means "fire when TurnsSinceLastFire reaches 2".
func TestApplyAfter_Cadence_EveryNTurns(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"cadence","message":"fired"}'`), 0755)

	proc := &Processor{
		Name: "cadenced",
		When: WhenConfig{
			On:          PhaseAgentResponded,
			Match:       MatchAll,
			StopReasons: []string{"end_turn"},
			Cadence:     &CadenceConfig{EveryNTurns: 2},
		},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	input := makeAfterInput("user", "end_turn")

	// Turn 1: pre-increment→1; 1 < 2 → skip
	r1 := m.ApplyAfter(context.Background(), input)
	if len(r1.Notifications) != 0 {
		t.Errorf("turn 1: expected 0 notifications (cadence not yet met), got %d", len(r1.Notifications))
	}

	// Turn 2: pre-increment→2; 2 >= 2 → fires! reset to 0
	r2 := m.ApplyAfter(context.Background(), input)
	if len(r2.Notifications) != 1 {
		t.Errorf("turn 2: expected 1 notification (cadence met), got %d", len(r2.Notifications))
	}

	// Turn 3: pre-increment→1; 1 < 2 → skip
	r3 := m.ApplyAfter(context.Background(), input)
	if len(r3.Notifications) != 0 {
		t.Errorf("turn 3: expected 0 notifications (cadence not yet met), got %d", len(r3.Notifications))
	}

	// Turn 4: pre-increment→2; 2 >= 2 → fires again
	r4 := m.ApplyAfter(context.Background(), input)
	if len(r4.Notifications) != 1 {
		t.Errorf("turn 4: expected 1 notification (cadence met), got %d", len(r4.Notifications))
	}

	// Turn 5: pre-increment→1; 1 < 2 → skip
	r5 := m.ApplyAfter(context.Background(), input)
	if len(r5.Notifications) != 0 {
		t.Errorf("turn 5: expected 0 notifications (cadence not yet met), got %d", len(r5.Notifications))
	}
}

// TestApplyAfter_AgentIdleGate verifies that an agentIdle processor fires only when
// the session is idle (SessionIdle=true) and is skipped while the agent is still
// draining its queue (SessionIdle=false).
func TestApplyAfter_AgentIdleGate(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"idle","message":"fired"}'`), 0755)

	proc := &Processor{
		Name:    "idle-proc",
		When:    WhenConfig{On: PhaseAgentIdle, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})

	// Busy turn: SessionIdle=false → should NOT fire.
	busy := makeAfterInput("user", "end_turn")
	busy.SessionIdle = false
	r1 := m.ApplyAfter(context.Background(), busy)
	if len(r1.Notifications) != 0 {
		t.Errorf("busy turn: expected 0 notifications (agent not idle), got %d", len(r1.Notifications))
	}

	// Idle turn: SessionIdle=true → should fire.
	idle := makeAfterInput("user", "end_turn")
	idle.SessionIdle = true
	r2 := m.ApplyAfter(context.Background(), idle)
	if len(r2.Notifications) != 1 {
		t.Errorf("idle turn: expected 1 notification (agent idle), got %d", len(r2.Notifications))
	}
}

// TestApplyAfter_AgentIdle_CadenceAccumulatesAcrossBurst verifies that an agentIdle
// processor's cadence counters keep accumulating on busy turns (so a queued burst counts
// toward the threshold) but the processor only fires once, at the idle breakpoint.
func TestApplyAfter_AgentIdle_CadenceAccumulatesAcrossBurst(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"idle","message":"fired"}'`), 0755)

	proc := &Processor{
		Name: "idle-cadence",
		When: WhenConfig{
			On:          PhaseAgentIdle,
			Match:       MatchAll,
			StopReasons: []string{"end_turn"},
			Cadence:     &CadenceConfig{EveryNTurns: 2},
		},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})

	busy := makeAfterInput("user", "end_turn")
	busy.SessionIdle = false
	idle := makeAfterInput("user", "end_turn")
	idle.SessionIdle = true

	// Turn 1 (busy): counter→1; gate 1<2 not met → skip.
	if r := m.ApplyAfter(context.Background(), busy); len(r.Notifications) != 0 {
		t.Errorf("turn 1 (busy): expected 0, got %d", len(r.Notifications))
	}
	// Turn 2 (busy): counter→2; gate met BUT not idle → skip, counters NOT reset.
	if r := m.ApplyAfter(context.Background(), busy); len(r.Notifications) != 0 {
		t.Errorf("turn 2 (busy, gate met): expected 0 (idle gate), got %d", len(r.Notifications))
	}
	// Turn 3 (idle): counter→3; gate met AND idle → FIRE, reset to 0.
	if r := m.ApplyAfter(context.Background(), idle); len(r.Notifications) != 1 {
		t.Errorf("turn 3 (idle): expected 1 (fires with full burst counted), got %d", len(r.Notifications))
	}
	// Turn 4 (idle): counter→1 after reset; gate 1<2 not met → skip.
	if r := m.ApplyAfter(context.Background(), idle); len(r.Notifications) != 0 {
		t.Errorf("turn 4 (idle): expected 0 (counter reset after firing), got %d", len(r.Notifications))
	}
}

// TestApplyAfter_Cadence_AfterInterval verifies that a processor with
// cadence.afterInterval fires only after the specified duration has passed.
func TestApplyAfter_Cadence_AfterInterval(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"interval","message":"fired"}'`), 0755)

	proc := &Processor{
		Name: "interval-proc",
		When: WhenConfig{
			On:          PhaseAgentResponded,
			Match:       MatchAll,
			StopReasons: []string{"end_turn"},
			Cadence:     &CadenceConfig{AfterInterval: "1h"},
		},
		Command: scriptPath,
		Output:  OutputNotify,
	}

	// Use a controllable clock.
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := makeAfterManager([]*Processor{proc})
	m.SetClock(func() time.Time { return fakeNow })
	input := makeAfterInput("user", "end_turn")

	// Turn 1: LastFiredAt is zero → interval threshold is skipped (nil time) → fires on first call
	r1 := m.ApplyAfter(context.Background(), input)
	if len(r1.Notifications) != 1 {
		t.Errorf("turn 1 (first ever): expected 1 notification (never fired before), got %d", len(r1.Notifications))
	}

	// Turn 2: only 0 seconds elapsed → interval not met → skip
	r2 := m.ApplyAfter(context.Background(), input)
	if len(r2.Notifications) != 0 {
		t.Errorf("turn 2: expected 0 notifications (interval not elapsed), got %d", len(r2.Notifications))
	}

	// Advance clock by 2 hours
	fakeNow = fakeNow.Add(2 * time.Hour)
	m.SetClock(func() time.Time { return fakeNow })

	// Turn 3: 2h elapsed, threshold is 1h → fires
	r3 := m.ApplyAfter(context.Background(), input)
	if len(r3.Notifications) != 1 {
		t.Errorf("turn 3: expected 1 notification (interval elapsed), got %d", len(r3.Notifications))
	}
}

// TestApplyAfter_Cadence_EveryNTokens verifies that a processor with
// cadence.everyNTokens fires only after enough cumulative tokens have been
// reported. This covers both real ACP usage and estimated token fallback
// (where only Total is set, Input/Output are zero).
func TestApplyAfter_Cadence_EveryNTokens(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"tokens","message":"fired"}'`), 0755)

	proc := &Processor{
		Name: "token-proc",
		When: WhenConfig{
			On:          PhaseAgentResponded,
			Match:       MatchAll,
			StopReasons: []string{"end_turn"},
			Cadence:     &CadenceConfig{EveryNTokens: 10000},
		},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})

	// Turn 1: 5000 tokens (below 10000 threshold) → skip
	input1 := makeAfterInput("user", "end_turn")
	input1.TokenUsage = &AfterTokenUsage{Total: 5000}
	r1 := m.ApplyAfter(context.Background(), input1)
	if len(r1.Notifications) != 0 {
		t.Errorf("turn 1: expected 0 notifications (5000 < 10000), got %d", len(r1.Notifications))
	}

	// Turn 2: another 6000 tokens (cumulative 11000 ≥ 10000) → fires
	input2 := makeAfterInput("user", "end_turn")
	input2.TokenUsage = &AfterTokenUsage{Total: 6000}
	r2 := m.ApplyAfter(context.Background(), input2)
	if len(r2.Notifications) != 1 {
		t.Errorf("turn 2: expected 1 notification (11000 >= 10000), got %d", len(r2.Notifications))
	}

	// Turn 3: 3000 tokens after reset (3000 < 10000) → skip
	input3 := makeAfterInput("user", "end_turn")
	input3.TokenUsage = &AfterTokenUsage{Total: 3000}
	r3 := m.ApplyAfter(context.Background(), input3)
	if len(r3.Notifications) != 0 {
		t.Errorf("turn 3: expected 0 notifications (3000 < 10000 after reset), got %d", len(r3.Notifications))
	}

	// Turn 4: estimated tokens only (Total set, Input/Output zero — fallback path)
	// 8000 more → cumulative 11000 ≥ 10000 → fires
	input4 := makeAfterInput("user", "end_turn")
	input4.TokenUsage = &AfterTokenUsage{Total: 8000} // simulates estimated fallback
	r4 := m.ApplyAfter(context.Background(), input4)
	if len(r4.Notifications) != 1 {
		t.Errorf("turn 4: expected 1 notification (11000 >= 10000 via estimated tokens), got %d", len(r4.Notifications))
	}
}

// TestApplyAfter_Cadence_EveryNTokens_NilUsage verifies that when TokenUsage
// is nil (no ACP usage AND no estimation), the token counter stays at zero
// and the everyNTokens threshold blocks the processor indefinitely.
func TestApplyAfter_Cadence_EveryNTokens_NilUsage(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"tokens","message":"fired"}'`), 0755)

	proc := &Processor{
		Name: "token-nil-proc",
		When: WhenConfig{
			On:          PhaseAgentResponded,
			Match:       MatchAll,
			StopReasons: []string{"end_turn"},
			Cadence:     &CadenceConfig{EveryNTokens: 1000},
		},
		Command: scriptPath,
		Output:  OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})

	// 5 turns with nil TokenUsage → token counter stays at 0 → never fires
	for i := 1; i <= 5; i++ {
		input := makeAfterInput("user", "end_turn")
		// input.TokenUsage is nil (default)
		r := m.ApplyAfter(context.Background(), input)
		if len(r.Notifications) != 0 {
			t.Errorf("turn %d: expected 0 notifications (nil usage), got %d", i, len(r.Notifications))
		}
	}
}

// TestApplyAfter_StatePersistence_MatchFirst verifies that match:first semantics
// survive a manager restart by using a shared MemoryStateStore.
func TestApplyAfter_StatePersistence_MatchFirst(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "notify.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"title":"first","message":"only once"}'`), 0755)

	proc := &Processor{
		Name:    "first-ever",
		When:    WhenConfig{On: PhaseAgentResponded, Match: MatchFirst, StopReasons: []string{"end_turn"}},
		Command: scriptPath,
		Output:  OutputNotify,
	}

	// Shared store simulates persistence across two Manager instances.
	shared := NewMemoryStateStore()
	input := makeAfterInput("user", "end_turn")

	// Session 1: first manager instance fires the processor.
	m1 := NewManager("", nil)
	m1.processors = []*Processor{proc}
	m1.SetStateStore(shared)
	r1 := m1.ApplyAfter(context.Background(), input)
	if len(r1.Notifications) != 1 {
		t.Fatalf("session 1: expected 1 notification, got %d", len(r1.Notifications))
	}

	// Session 2: new manager instance — but same shared store.
	// Should NOT fire because AgentResponseCount == 1 in the store.
	m2 := NewManager("", nil)
	m2.processors = []*Processor{proc}
	m2.SetStateStore(shared)
	r2 := m2.ApplyAfter(context.Background(), input)
	if len(r2.Notifications) != 0 {
		t.Errorf("session 2 (after restart): expected 0 notifications (match=first already fired), got %d", len(r2.Notifications))
	}
}

// TestCadenceConfig_Validation verifies that invalid cadence configurations
// are rejected by the loader.
func TestCadenceConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cadence *CadenceConfig
		wantErr string
	}{
		{
			name:    "no thresholds",
			cadence: &CadenceConfig{},
			wantErr: "at least one of",
		},
		{
			name:    "negative everyNTurns",
			cadence: &CadenceConfig{EveryNTurns: -1},
			wantErr: "everyNTurns",
		},
		{
			name:    "negative everyNTokens",
			cadence: &CadenceConfig{EveryNTokens: -1},
			wantErr: "everyNTokens",
		},
		{
			name:    "invalid duration",
			cadence: &CadenceConfig{AfterInterval: "not-a-duration"},
			wantErr: "afterInterval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlContent := buildProcessorYAML(tt.cadence)
			tmpDir := t.TempDir()
			f, _ := os.CreateTemp(tmpDir, "*.yaml")
			f.WriteString(yamlContent)
			f.Close()
			loader := NewLoader(tmpDir, nil)
			// The loader now skips invalid documents (lenient behavior) instead
			// of returning an error. A bad cadence config should cause the
			// document to be skipped, so LoadFile returns nil, nil.
			proc, err := loader.LoadFile(f.Name())
			if err != nil {
				t.Fatalf("LoadFile() returned unexpected error: %v", err)
			}
			if proc != nil {
				t.Fatalf("expected invalid cadence config %q to be skipped (nil proc), but got processor %q", tt.name, proc.Name)
			}
		})
	}
}

// TestExecutorExecuteRawOutput verifies that a command-mode processor with outputFormat:raw
// returns the trimmed stdout as both Message and Text (no JSON parsing required).
func TestExecutorExecuteRawOutput(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "raw.sh")
	scriptContent := "#!/bin/sh\nprintf '  ## Beads Memories\\n\\nSome project context.  '\n"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	executor := NewExecutor(tmpDir, nil)
	proc := &Processor{
		Name:         "raw-output-test",
		Command:      scriptPath,
		Output:       OutputPrepend,
		OutputFormat: OutputFormatRaw,
		Input:        InputNone,
		HookDir:      tmpDir,
	}
	input := &ProcessorInput{
		Message:    "user question",
		WorkingDir: tmpDir,
	}

	ctx := context.Background()
	output, err := executor.Execute(ctx, proc, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "## Beads Memories\n\nSome project context."
	if output.Message != want {
		t.Errorf("Execute() Message = %q, want %q", output.Message, want)
	}
	if output.Text != want {
		t.Errorf("Execute() Text = %q, want %q", output.Text, want)
	}
}

// TestApplyWithRerun_PrependPreservesOriginalMessage is a regression test for the bug where
// the applyWithRerun command-mode path ignored output type and replaced the message entirely.
// A command-mode processor with output:prepend + outputFormat:raw must PREPEND to the user
// message and PRESERVE the original — not replace it.
func TestApplyWithRerun_PrependPreservesOriginalMessage(t *testing.T) {
	tmpDir := t.TempDir()

	// Script outputs raw text (not JSON) simulating `bd prime --memories-only`
	scriptPath := filepath.Join(tmpDir, "inject.sh")
	scriptContent := "#!/bin/sh\necho '[INJECTED CONTEXT]'\n"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	// We need at least one prompt-mode processor to force the applyWithRerun path.
	mgr := NewManager(tmpDir, nil)
	mgr.processors = []*Processor{
		// Prompt-mode processor forces applyWithRerun path for ALL processors
		{
			Name:   "force-rerun-path",
			When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Prompt: "Analyze: @mitto:session_id",
		},
		// Command-mode prepend processor — this is the bug-under-test
		{
			Name:         "inject-context",
			When:         WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
			Command:      scriptPath,
			Output:       OutputPrepend,
			OutputFormat: OutputFormatRaw,
			Input:        InputNone,
			HookDir:      tmpDir,
			Priority:     50, // run before prompt-mode
		},
	}

	// SetPromptFunc to handle the prompt-mode processor (fire-and-forget)
	mgr.SetPromptFunc(func(ctx context.Context, wsUUID, procName, prompt string) error {
		return nil
	})

	input := &ProcessorInput{
		Message:        "original user message",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
		WorkspaceUUID:  "ws-test",
	}

	ctx := context.Background()
	result, err := mgr.Apply(ctx, input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Original message must be preserved (prepended to, not replaced)
	if !strings.Contains(result.Message, "original user message") {
		t.Errorf("original message lost; got %q", result.Message)
	}
	// Injected context must appear before the original message
	if !strings.HasPrefix(result.Message, "[INJECTED CONTEXT]") {
		t.Errorf("expected injected context prepended; got %q", result.Message)
	}
}

// TestLoader_OutputFormatValidation verifies the outputFormat validation rules.
func TestLoader_OutputFormatValidation(t *testing.T) {
	t.Run("invalid outputFormat rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "bad.yaml", `
name: bad-format
when:
  on: userPrompt
  match: all
command: /bin/echo
outputFormat: bogus
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() should not error (bad files skipped): %v", err)
		}
		if len(procs) != 0 {
			t.Errorf("expected 0 processors (bad outputFormat skipped), got %d", len(procs))
		}
	})

	t.Run("outputFormat on text-mode processor rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "bad.yaml", `
name: text-with-format
when:
  on: userPrompt
  match: all
text: "hello"
mutate: prepend
outputFormat: raw
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() should not error (bad files skipped): %v", err)
		}
		if len(procs) != 0 {
			t.Errorf("expected 0 processors (outputFormat on text-mode skipped), got %d", len(procs))
		}
	})

	t.Run("valid outputFormat:raw on command-mode loads OK", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "good.yaml", `
name: raw-command
when:
  on: userPrompt
  match: all
command: /bin/echo
output: prepend
outputFormat: raw
input: none
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(procs) != 1 {
			t.Fatalf("expected 1 processor, got %d", len(procs))
		}
		if procs[0].GetOutputFormat() != OutputFormatRaw {
			t.Errorf("expected OutputFormatRaw, got %q", procs[0].GetOutputFormat())
		}
	})

	t.Run("valid outputFormat:json on command-mode loads OK", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "good.yaml", `
name: json-command
when:
  on: userPrompt
  match: all
command: /bin/echo
outputFormat: json
input: none
`)
		loader := NewLoader(dir, nil)
		procs, err := loader.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(procs) != 1 {
			t.Fatalf("expected 1 processor, got %d", len(procs))
		}
		if procs[0].GetOutputFormat() != OutputFormatJSON {
			t.Errorf("expected OutputFormatJSON, got %q", procs[0].GetOutputFormat())
		}
	})
}

// TestBuiltinProcessorsValidity walks every embedded builtin YAML and asserts that
// LoadFile parses and validates it without error. This guards all builtins including
// the new beads-prime.yaml.
func TestBuiltinProcessorsValidity(t *testing.T) {
	filenames, err := rootconfig.ListEmbeddedProcessors()
	if err != nil {
		t.Fatalf("ListEmbeddedProcessors() error = %v", err)
	}
	if len(filenames) == 0 {
		t.Fatal("no embedded builtin processors found; check config/processors/builtin/")
	}

	for _, filename := range filenames {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			// Read the embedded file content
			srcPath := rootconfig.BuiltinProcessorsDir + "/" + filename
			content, err := rootconfig.BuiltinProcessorsFS.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", srcPath, err)
			}

			// Write to a temp dir and load via the Loader (full validation path)
			dir := t.TempDir()
			destPath := filepath.Join(dir, filename)
			if err := os.WriteFile(destPath, content, 0644); err != nil {
				t.Fatalf("WriteFile error = %v", err)
			}

			loader := NewLoader(dir, nil)
			procs, err := loader.Load()
			if err != nil {
				t.Fatalf("Load() returned unexpected error: %v", err)
			}
			if len(procs) == 0 {
				t.Errorf("Load() returned 0 processors (file may have failed validation); check loader warnings")
			}
		})
	}
}

// buildProcessorYAML builds a minimal processor YAML with the given cadence config for testing.
// When cadence is non-nil, ALL fields are always written (including zeros) so the YAML
// parser sees an explicit cadence block (not null). This lets us test validation of
// configurations that have cadence present but all thresholds at zero.
func buildProcessorYAML(cadence *CadenceConfig) string {
	var sb strings.Builder
	sb.WriteString("name: test-cadence\n")
	sb.WriteString("command: /bin/true\n")
	sb.WriteString("when:\n")
	sb.WriteString("  on: agentResponded\n")
	sb.WriteString("  match: all\n")
	if cadence != nil {
		sb.WriteString("  cadence:\n")
		// Always write all fields explicitly so the block is not parsed as null.
		fmt.Fprintf(&sb, "    everyNTurns: %d\n", cadence.EveryNTurns)
		fmt.Fprintf(&sb, "    everyNTokens: %d\n", cadence.EveryNTokens)
		if cadence.AfterInterval != "" {
			fmt.Fprintf(&sb, "    afterInterval: %q\n", cadence.AfterInterval)
		} else {
			sb.WriteString("    afterInterval: \"\"\n")
		}
	}
	return sb.String()
}


// TestBuildCELContext_Iteration verifies that BuildCELContext correctly populates
// the ctx.Iteration.* fields from ProcessorInput.IterationNumber / MaxIterations / IsPeriodic.
func TestBuildCELContext_Iteration(t *testing.T) {
	cases := []struct {
		name                   string
		isPeriodic             bool
		iterationNum           int
		maxIterations          int
		iterationUninterrupted bool
		wantIsFirst            bool
		wantIsLast             bool
		wantIsUninterrupted    bool
	}{
		// (1) First run of a 3-run periodic sequence.
		{
			name:          "first-of-three",
			isPeriodic:    true,
			iterationNum:  0,
			maxIterations: 3,
			wantIsFirst:   true,
			wantIsLast:    false,
		},
		// (2) Last run of a 3-run periodic sequence.
		{
			name:          "last-of-three",
			isPeriodic:    true,
			iterationNum:  2,
			maxIterations: 3,
			wantIsFirst:   false,
			wantIsLast:    true,
		},
		// (3) Unlimited sequence (Max=0) — IsLast must always be false.
		{
			name:          "unlimited",
			isPeriodic:    true,
			iterationNum:  5,
			maxIterations: 0,
			wantIsFirst:   false,
			wantIsLast:    false,
		},
		// (4) Uninterrupted continuation (mitto-5xjn).
		{
			name:                   "uninterrupted",
			isPeriodic:             true,
			iterationNum:           3,
			maxIterations:          0,
			iterationUninterrupted: true,
			wantIsFirst:            false,
			wantIsLast:             false,
			wantIsUninterrupted:    true,
		},
		// (5) Interrupted (user prompt between runs) — IsUninterrupted must be false.
		{
			name:                   "interrupted",
			isPeriodic:             true,
			iterationNum:           3,
			maxIterations:          0,
			iterationUninterrupted: false,
			wantIsFirst:            false,
			wantIsLast:             false,
			wantIsUninterrupted:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := &ProcessorInput{
				SessionID:              "sess-iter",
				IsPeriodic:             tc.isPeriodic,
				IterationNumber:        tc.iterationNum,
				MaxIterations:          tc.maxIterations,
				IterationUninterrupted: tc.iterationUninterrupted,
			}
			ctx := BuildCELContext(input)

			if ctx.Iteration.Number != tc.iterationNum {
				t.Errorf("Number: got %d, want %d", ctx.Iteration.Number, tc.iterationNum)
			}
			if ctx.Iteration.Max != tc.maxIterations {
				t.Errorf("Max: got %d, want %d", ctx.Iteration.Max, tc.maxIterations)
			}
			if ctx.Iteration.IsPeriodic != tc.isPeriodic {
				t.Errorf("IsPeriodic: got %v, want %v", ctx.Iteration.IsPeriodic, tc.isPeriodic)
			}
			if ctx.Iteration.IsFirst != tc.wantIsFirst {
				t.Errorf("IsFirst: got %v, want %v", ctx.Iteration.IsFirst, tc.wantIsFirst)
			}
			if ctx.Iteration.IsLast != tc.wantIsLast {
				t.Errorf("IsLast: got %v, want %v", ctx.Iteration.IsLast, tc.wantIsLast)
			}
			if ctx.Iteration.IsUninterrupted != tc.wantIsUninterrupted {
				t.Errorf("IsUninterrupted: got %v, want %v", ctx.Iteration.IsUninterrupted, tc.wantIsUninterrupted)
			}
		})
	}
}
