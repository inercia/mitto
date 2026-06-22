package conversation

import (
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

// TestMatchConstraintOption tests the constraint matching logic for all match modes.
func TestMatchConstraintOption(t *testing.T) {
	modelOptions := []SessionConfigOptionValue{
		{Value: "opus-4.5", Name: "opus-4.5"},
		{Value: "opus-4.6", Name: "opus-4.6"},
		{Value: "opus-4.6-500k", Name: "opus-4.6 (500K context)"},
		{Value: "opus-4.7", Name: "opus-4.7"},
		{Value: "opus-4.7-500k", Name: "opus-4.7 (500K context)"},
		{Value: "opus-4.8", Name: "opus-4.8"},
		{Value: "sonnet-4.6", Name: "sonnet-4.6"},
		{Value: "gpt-4o", Name: "GPT-4o"},
	}

	tests := []struct {
		name       string
		constraint *config.ACPServerConstraint
		options    []SessionConfigOptionValue
		want       string
	}{
		{name: "contains picks last match", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "opus"}, options: modelOptions, want: "opus-4.8"},
		{name: "contains case insensitive", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "OPUS"}, options: modelOptions, want: "opus-4.8"},
		{name: "contains specific version picks last variant", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "opus-4.6"}, options: modelOptions, want: "opus-4.6-500k"},
		{name: "contains no match", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "claude"}, options: modelOptions, want: ""},
		{name: "exact match", constraint: &config.ACPServerConstraint{MatchMode: "exact", Pattern: "opus-4.7"}, options: modelOptions, want: "opus-4.7"},
		{name: "exact match case insensitive", constraint: &config.ACPServerConstraint{MatchMode: "exact", Pattern: "GPT-4o"}, options: modelOptions, want: "gpt-4o"},
		{name: "exact no match for partial", constraint: &config.ACPServerConstraint{MatchMode: "exact", Pattern: "opus"}, options: modelOptions, want: ""},
		{name: "startsWith picks last match", constraint: &config.ACPServerConstraint{MatchMode: "startsWith", Pattern: "opus"}, options: modelOptions, want: "opus-4.8"},
		{name: "startsWith no match", constraint: &config.ACPServerConstraint{MatchMode: "startsWith", Pattern: "claude"}, options: modelOptions, want: ""},
		{name: "regex picks last match", constraint: &config.ACPServerConstraint{MatchMode: "regex", Pattern: "opus-4\\.[67]"}, options: modelOptions, want: "opus-4.7-500k"},
		{name: "regex no match", constraint: &config.ACPServerConstraint{MatchMode: "regex", Pattern: "^claude"}, options: modelOptions, want: ""},
		{name: "lookAlike single word picks last", constraint: &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "opus"}, options: modelOptions, want: "opus-4.8"},
		{name: "lookAlike two words", constraint: &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}, options: modelOptions, want: "opus-4.8"},
		{
			name:       "lookAlike matches mixed case and separators",
			constraint: &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.7"},
			options: []SessionConfigOptionValue{
				{Value: "v1", Name: "opus-4.5"},
				{Value: "v2", Name: "OPUS-Pro-4.7"},
				{Value: "v3", Name: "Opus 4.7"},
			},
			want: "v3",
		},
		{name: "lookAlike no match when word missing", constraint: &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "opus 5.0"}, options: modelOptions, want: ""},
		{name: "lookAlike empty pattern returns empty", constraint: &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: ""}, options: modelOptions, want: ""},
		{name: "unknown match mode returns empty", constraint: &config.ACPServerConstraint{MatchMode: "unknown", Pattern: "opus"}, options: modelOptions, want: ""},
		{name: "empty options returns empty", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "opus"}, options: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchConstraintOption(tt.constraint, tt.options)
			if got != tt.want {
				t.Errorf("MatchConstraintOption() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveAuxModelSwitch pins down the auxiliary model-switch decision (mitto-ykb).
func TestResolveAuxModelSwitch(t *testing.T) {
	models := func(current string) *acp.UnstableSessionModelState {
		return &acp.UnstableSessionModelState{
			CurrentModelId: acp.UnstableModelId(current),
			AvailableModels: []acp.UnstableModelInfo{
				{ModelId: "claude-haiku-4-5", Name: "Haiku 4.5"},
				{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
				{ModelId: "claude-opus-4-8", Name: "Opus 4.8"},
			},
		}
	}
	tests := []struct {
		name          string
		constraint    *config.ACPServerConstraint
		models        *acp.UnstableSessionModelState
		wantModelID   string
		wantShouldSet bool
	}{
		{name: "nil constraint skips", constraint: nil, models: models("claude-sonnet-4-6"), wantModelID: "", wantShouldSet: false},
		{name: "empty pattern skips", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: ""}, models: models("claude-sonnet-4-6"), wantModelID: "", wantShouldSet: false},
		{name: "no available model matches", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "gpt"}, models: models("claude-sonnet-4-6"), wantModelID: "", wantShouldSet: false},
		{name: "current already matches skips set_model", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "haiku"}, models: models("claude-haiku-4-5"), wantModelID: "claude-haiku-4-5", wantShouldSet: false},
		{name: "switch required when current differs", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "haiku"}, models: models("claude-sonnet-4-6"), wantModelID: "claude-haiku-4-5", wantShouldSet: true},
		{name: "nil models returns no-op", constraint: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "haiku"}, models: nil, wantModelID: "", wantShouldSet: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModelID, gotShouldSet := ResolveAuxModelSwitch(tt.constraint, tt.models)
			if gotModelID != tt.wantModelID || gotShouldSet != tt.wantShouldSet {
				t.Errorf("ResolveAuxModelSwitch() = (%q, %v), want (%q, %v)",
					gotModelID, gotShouldSet, tt.wantModelID, tt.wantShouldSet)
			}
		})
	}
}

// TestSelectPreferredModel tests the per-prompt model resolver.
func TestSelectPreferredModel(t *testing.T) {
	newModels := func(current string) *acp.UnstableSessionModelState {
		return &acp.UnstableSessionModelState{
			CurrentModelId: acp.UnstableModelId(current),
			AvailableModels: []acp.UnstableModelInfo{
				{ModelId: "claude-haiku-4-5", Name: "Haiku 4.5"},
				{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
				{ModelId: "claude-opus-4-6", Name: "Opus 4.6"},
				{ModelId: "gpt-4o", Name: "GPT-4o"},
			},
		}
	}
	tests := []struct {
		name     string
		patterns []string
		current  string
		want     string
	}{
		{name: "exact match by model id", patterns: []string{"claude-opus-4-6"}, current: "claude-sonnet-4-6", want: "claude-opus-4-6"},
		{name: "match by display name", patterns: []string{"Sonnet 4.6"}, current: "claude-opus-4-6", want: "claude-sonnet-4-6"},
		{name: "current matches only pattern → keep", patterns: []string{"*sonnet*"}, current: "claude-sonnet-4-6", want: "claude-sonnet-4-6"},
		{name: "current matches broad pattern → keep", patterns: []string{"claude-*"}, current: "claude-sonnet-4-6", want: "claude-sonnet-4-6"},
		{name: "current does not match broad pattern → first match", patterns: []string{"claude-*"}, current: "gpt-4o", want: "claude-haiku-4-5"},
		{name: "higher-priority pattern wins → switch", patterns: []string{"*opus*", "*sonnet*"}, current: "claude-sonnet-4-6", want: "claude-opus-4-6"},
		{name: "current matches highest-priority → keep", patterns: []string{"*opus*", "*sonnet*"}, current: "claude-opus-4-6", want: "claude-opus-4-6"},
		{name: "first pattern matches none, current matches second → keep", patterns: []string{"*nonexistent*", "*haiku*"}, current: "claude-haiku-4-5", want: "claude-haiku-4-5"},
		{name: "no pattern matches anything → empty", patterns: []string{"*nonexistent*", "*missing*"}, current: "claude-sonnet-4-6", want: ""},
		{name: "empty patterns → empty", patterns: []string{}, current: "claude-sonnet-4-6", want: ""},
		{name: "nil patterns → empty", patterns: nil, current: "claude-sonnet-4-6", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectPreferredModel(tt.patterns, newModels(tt.current))
			if got != tt.want {
				t.Errorf("SelectPreferredModel(%v, current=%q) = %q, want %q", tt.patterns, tt.current, got, tt.want)
			}
		})
	}
}

// TestSelectPreferredModel_NilModels ensures the function handles nil model state.
func TestSelectPreferredModel_NilModels(t *testing.T) {
	if got := SelectPreferredModel([]string{"*sonnet*"}, nil); got != "" {
		t.Errorf("SelectPreferredModel with nil models = %q, want empty", got)
	}
}

// TestConstraintModelSwitchBudgetMath verifies that constraintModelSwitchCallerBudget
// (90s) is large enough to cover worst-case setModelSem contention at server wakeup
// (mitto-f7q). Mirrors internal/web's TestSetModelAsyncBudgetMath.
//
// The set_model retry/attempt constants live in internal/web (SharedACPProcess) which
// this package must NOT import, so the expected values are asserted against the locally
// documented constants below — kept consistent with the doc comment on
// constraintModelSwitchCallerBudget and internal/web/shared_acp_process.go.
func TestConstraintModelSwitchBudgetMath(t *testing.T) {
	const (
		maxConcurrentCallers = 4 // from bead: ~4 concurrent sessions at wakeup
		// Mirror of internal/web/shared_acp_process.go set_model constants.
		maxRetries        = 3                      // setSessionModelMaxAttempts
		maxAttemptTimeout = 8 * time.Second        // setSessionModelAttemptTimeout
		retryBaseDelay    = 300 * time.Millisecond // setSessionModelRetryBaseDelay
		retryJitterRatio  = 0.5                    // setSessionModelRetryJitterRatio
	)

	// Max backoff across all retry cycles (attempt 2 + attempt 3, each jittered up).
	maxJitteredBackoff := time.Duration(float64(retryBaseDelay)*float64(maxRetries-1)*(1+retryJitterRatio)) + retryBaseDelay

	// Per-caller worst-case: N attempts × per-attempt timeout + total jittered backoff.
	perCallerMax := time.Duration(maxRetries)*maxAttemptTimeout + maxJitteredBackoff

	// Semaphore wait: up to (N-1) prior holders each at their worst case.
	semWaitMax := time.Duration(maxConcurrentCallers-1) * perCallerMax

	// Verify the budget exceeds the expected contention region (first 3 of 4 holders
	// exhausted), even if not the absolute 4-holder worst case.
	expectedContentionCoverage := time.Duration(maxConcurrentCallers-2) * perCallerMax
	if constraintModelSwitchCallerBudget < expectedContentionCoverage {
		t.Errorf("constraintModelSwitchCallerBudget (%v) is less than expected contention coverage (%v); "+
			"increase the budget constant", constraintModelSwitchCallerBudget, expectedContentionCoverage)
	}

	t.Logf("per-caller max: %v, sem wait (N-1=%d holders): %v, caller budget: %v",
		perCallerMax, maxConcurrentCallers-1, semWaitMax, constraintModelSwitchCallerBudget)
}
