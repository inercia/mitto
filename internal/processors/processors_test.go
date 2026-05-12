package processors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

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
			name: "enabledWhen CEL matches acp.name",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `acp.name == "auggie-opus"`},
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
			name: "enabledWhen CEL acp.name no match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `acp.name == "auggie-opus"`},
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
			name: "enabledWhen CEL matches acp.tags",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `acp.tags.exists(t, t == "reasoning")`},
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
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `acp.tags.exists(t, t == "reasoning")`},
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
			name: "enabledWhen CEL children.exists",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `children.exists`},
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "child-1", Name: "Sub task"},
				},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "enabledWhen CEL children.exists false",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `children.exists`},
			input:          &ProcessorInput{},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name: "enabledWhen CEL children.mcp_count threshold met",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `children.mcp_count >= 2`},
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
			name: "enabledWhen CEL children.mcp_count below threshold",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `children.mcp_count >= 2`},
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
			name: "tools.hasAllPatterns all patterns satisfied",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasAllPatterns(["mitto_*", "jira_*"])`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list", "jira_search"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "tools.hasAllPatterns some patterns not satisfied",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasAllPatterns(["mitto_*", "slack_*"])`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "jira_search"},
			},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "tools.hasPattern no tools available",
			hook:           &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasPattern("mitto_*")`},
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
			name: "tools.hasPattern exact tool match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasPattern("mitto_conversation_new")`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL tools.hasPattern",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasPattern("mitto_*")`},
			input: &ProcessorInput{
				MCPToolNames: []string{"mitto_conversation_new", "mitto_conversation_list"},
			},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name: "enabledWhen CEL tools.hasPattern no match",
			hook: &Processor{When: WhenConfig{On: PhaseUserPrompt, Match: MatchAll}, EnabledWhen: `tools.hasPattern("slack_*")`},
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
	expected := "PREFIX: original"
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
	expected := "original :SUFFIX"
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
	// Message should remain unchanged
	if result.Message != "original" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "original")
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
	// Message should remain unchanged
	if result.Message != "original" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "original")
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
	if result.Message != "PREFIX: hello world" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "PREFIX: hello world")
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
	if result.Message != "hello world SUFFIX" {
		t.Errorf("ApplyProcessors() = %q, want %q", result.Message, "hello world SUFFIX")
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
	expected := "Context: user message\n---\nEnd"
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
	if result.Message != "FIRST: msg" {
		t.Errorf("first message: got %q, want %q", result.Message, "FIRST: msg")
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

	// At this point, @mitto: variables are still unresolved
	expectedBeforeSubst := "Session: @mitto:session_id\nProject: @mitto:working_dir\n\nFix the login bug\n[agent: @mitto:acp_server]"
	if result.Message != expectedBeforeSubst {
		t.Errorf("before substitution: got %q, want %q", result.Message, expectedBeforeSubst)
	}

	// Step 2: Substitute variables (as BackgroundSession does)
	finalMessage := SubstituteVariables(result.Message, input)

	expectedAfterSubst := "Session: sess-001\nProject: /home/user/myproject\n\nFix the login bug\n[agent: claude-code]"
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

	// Empty parent_session_id should substitute to empty string
	expected := "Parent: \nhello"
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

	expected := "Available: auggie [coding] (current), claude-code [fast]\n\ndo something"
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
	// Prompt-mode doesn't modify the message (fire-and-forget)
	if result.Message != "hello" {
		t.Errorf("expected message unchanged, got %q", result.Message)
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

// makeAfterInput returns a minimal AfterProcessorInput for tests.
// SessionDir is set to a stable key so the MemoryStateStore injected by
// makeAfterManager shares state across successive calls within the same test.
func makeAfterInput(origin, stopReason string) AfterProcessorInput {
	return AfterProcessorInput{
		SessionID:     "test-session",
		SessionDir:    "test-session-dir", // key for MemoryStateStore
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
func TestApplyAfter_PromptMode_Notify(t *testing.T) {
	proc := &Processor{
		Name:   "prompt-notifier",
		When:   WhenConfig{On: PhaseAgentResponded, Match: MatchAll, StopReasons: []string{"end_turn"}},
		Prompt: `{"title":"Stop: @mitto:stop_reason","message":"Origin: @mitto:origin"}`,
		Output: OutputNotify,
	}
	m := makeAfterManager([]*Processor{proc})
	input := makeAfterInput("user", "end_turn")
	result := m.ApplyAfter(context.Background(), input)

	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.Notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(result.Notifications))
	}
	n := result.Notifications[0]
	if n.Title != "Stop: end_turn" {
		t.Errorf("expected 'Stop: end_turn', got %q", n.Title)
	}
	if n.Message != "Origin: user" {
		t.Errorf("expected 'Origin: user', got %q", n.Message)
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
			_, err := loader.LoadFile(f.Name())
			if err == nil {
				t.Fatalf("expected validation error for %q, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
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
		sb.WriteString(fmt.Sprintf("    everyNTurns: %d\n", cadence.EveryNTurns))
		sb.WriteString(fmt.Sprintf("    everyNTokens: %d\n", cadence.EveryNTokens))
		if cadence.AfterInterval != "" {
			sb.WriteString(fmt.Sprintf("    afterInterval: %q\n", cadence.AfterInterval))
		} else {
			sb.WriteString("    afterInterval: \"\"\n")
		}
	}
	return sb.String()
}
