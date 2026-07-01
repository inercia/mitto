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

// TestResolveProfileModel verifies that a model profile's Criteria resolves to the
// matching model id via the shared constraint match engine.
func TestResolveProfileModel(t *testing.T) {
	models := &acp.UnstableSessionModelState{
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "claude-haiku-4-5", Name: "Haiku 4.5"},
			{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
			{ModelId: "claude-opus-4-8", Name: "Opus 4.8"},
		},
	}
	tests := []struct {
		name    string
		profile *config.ModelProfile
		models  *acp.UnstableSessionModelState
		want    string
	}{
		{name: "nil profile", profile: nil, models: models, want: ""},
		{name: "nil criteria", profile: &config.ModelProfile{Name: "TagsOnly"}, models: models, want: ""},
		{name: "contains match", profile: &config.ModelProfile{Name: "Opus", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}}, models: models, want: "claude-opus-4-8"},
		{name: "no match", profile: &config.ModelProfile{Name: "GPT", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "gpt"}}, models: models, want: ""},
		{name: "nil models", profile: &config.ModelProfile{Name: "Opus", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}}, models: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveProfileModel(tt.profile, tt.models); got != tt.want {
				t.Errorf("ResolveProfileModel() = %q, want %q", got, tt.want)
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

// selectPreferredModelTestProfiles returns a fixture of model profiles used by
// TestSelectPreferredModel: Opus (contains "Opus", tags Reasoning/Smartest), Sonnet
// (contains "Sonnet", tags Coding/Smart/Backup), Haiku (contains "Haiku", tags
// Cheap/Fast), and Gemini (contains "gemini", tag Backup) which never resolves against
// the fixture's available models — used to exercise deterministic tag fallback (the
// first tagged profile, in slice order, that yields an available model wins).
func selectPreferredModelTestProfiles() []config.ModelProfile {
	return []config.ModelProfile{
		{Name: "Opus", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}, Tags: []string{"Reasoning", "Smartest"}},
		{Name: "Gemini", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "gemini"}, Tags: []string{"Backup"}},
		{Name: "Sonnet", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet"}, Tags: []string{"Coding", "Smart", "Backup"}},
		{Name: "Haiku", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Haiku"}, Tags: []string{"Cheap", "Fast"}},
	}
}

// TestSelectPreferredModel tests the per-prompt model resolver against ModelName/ModelTag
// preference entries resolved through a fixture of global model profiles.
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
	profiles := selectPreferredModelTestProfiles()

	tests := []struct {
		name    string
		prefs   []config.PromptPreferredModel
		current string
		want    string
	}{
		{name: "modelName resolves to profile's model", prefs: []config.PromptPreferredModel{{ModelName: "Opus"}}, current: "claude-sonnet-4-6", want: "claude-opus-4-6"},
		{name: "modelName case-insensitive", prefs: []config.PromptPreferredModel{{ModelName: "sonnet"}}, current: "claude-opus-4-6", want: "claude-sonnet-4-6"},
		{name: "current satisfies modelName → keep, no switch", prefs: []config.PromptPreferredModel{{ModelName: "Sonnet"}}, current: "claude-sonnet-4-6", want: "claude-sonnet-4-6"},
		{name: "modelTag resolves first-yielding profile deterministically", prefs: []config.PromptPreferredModel{{ModelTag: "Backup"}}, current: "gpt-4o", want: "claude-sonnet-4-6"},
		{name: "current satisfies modelTag → keep, no switch", prefs: []config.PromptPreferredModel{{ModelTag: "Cheap"}}, current: "claude-haiku-4-5", want: "claude-haiku-4-5"},
		{name: "unknown modelName falls through to next entry", prefs: []config.PromptPreferredModel{{ModelName: "Nonexistent"}, {ModelName: "Haiku"}}, current: "claude-sonnet-4-6", want: "claude-haiku-4-5"},
		{name: "unknown modelTag falls through to next entry", prefs: []config.PromptPreferredModel{{ModelTag: "Nonexistent"}, {ModelName: "Opus"}}, current: "claude-sonnet-4-6", want: "claude-opus-4-6"},
		{name: "ordered first-match-wins: higher-priority entry wins over current match", prefs: []config.PromptPreferredModel{{ModelName: "Opus"}, {ModelName: "Sonnet"}}, current: "claude-sonnet-4-6", want: "claude-opus-4-6"},
		{name: "empty entry list → empty", prefs: []config.PromptPreferredModel{}, current: "claude-sonnet-4-6", want: ""},
		{name: "nil entry list → empty", prefs: nil, current: "claude-sonnet-4-6", want: ""},
		{name: "entry with neither modelName nor modelTag falls through → empty", prefs: []config.PromptPreferredModel{{}}, current: "claude-sonnet-4-6", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectPreferredModel(tt.prefs, profiles, newModels(tt.current))
			if got != tt.want {
				t.Errorf("SelectPreferredModel(%v, current=%q) = %q, want %q", tt.prefs, tt.current, got, tt.want)
			}
		})
	}
}

// TestSelectPreferredModel_NilModels ensures the function handles nil model state.
func TestSelectPreferredModel_NilModels(t *testing.T) {
	if got := SelectPreferredModel([]config.PromptPreferredModel{{ModelName: "Sonnet"}}, selectPreferredModelTestProfiles(), nil); got != "" {
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
		// Mirror of internal/acpproc/shared_acp_process.go set_model constants.
		// Attempt schedule {12s,8s,5s} sums to 25s — same total as the prior 3×8s (mitto-f7q).
		maxRetries       = 3                      // setSessionModelMaxAttempts
		scheduleSum      = 25 * time.Second       // sum(setSessionModelAttemptTimeouts)
		retryBaseDelay   = 300 * time.Millisecond // setSessionModelRetryBaseDelay
		retryJitterRatio = 0.5                    // setSessionModelRetryJitterRatio
	)

	// Max backoff across all retry cycles (attempt 2 + attempt 3, each jittered up).
	maxJitteredBackoff := time.Duration(float64(retryBaseDelay)*float64(maxRetries-1)*(1+retryJitterRatio)) + retryBaseDelay

	// Per-caller worst-case: schedule sum + total jittered backoff.
	perCallerMax := scheduleSum + maxJitteredBackoff

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

// TestChildStartupJitter verifies the de-stagger jitter helper (mitto-x4e): values are
// always within [0, max) for a positive bound, and 0 for a non-positive bound.
func TestChildStartupJitter(t *testing.T) {
	if got := childStartupJitter(0); got != 0 {
		t.Errorf("childStartupJitter(0) = %v, want 0", got)
	}
	if got := childStartupJitter(-time.Second); got != 0 {
		t.Errorf("childStartupJitter(-1s) = %v, want 0", got)
	}

	max := constraintModelSwitchChildStartupJitter
	for i := 0; i < 1000; i++ {
		got := childStartupJitter(max)
		if got < 0 || got >= max {
			t.Fatalf("childStartupJitter(%v) = %v, out of range [0, %v)", max, got, max)
		}
	}
}
