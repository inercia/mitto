package conversation

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

// TestModelStateFromACP_Nil verifies the converter is safe on a nil input,
// which is the shape of resp.Models when the agent omits the top-level `models`
// field (spec-compliant v0.13.5 agents).
func TestModelStateFromACP_Nil(t *testing.T) {
	if got := ModelStateFromACP(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}
}

// TestModelStateFromACP_EmptyAvailable ensures a src with zero available models
// yields nil so the caller doesn't emit an empty synthetic config option (which
// would flip has_model_option=true with an empty selector).
func TestModelStateFromACP_EmptyAvailable(t *testing.T) {
	src := &acp.SessionModelState{
		CurrentModelId:  acp.ModelId("m-1"),
		AvailableModels: nil,
	}
	if got := ModelStateFromACP(src); got != nil {
		t.Fatalf("expected nil for empty AvailableModels, got %+v", got)
	}
}

// TestModelStateFromACP_Populated exercises the happy path that mitto-i8n
// depends on: Auggie ships its catalog under the top-level `models` field, and
// the converter must faithfully carry ModelId, Name and Description across the
// SDK/Mitto boundary (ModelId is a typed string alias upstream, plain string
// locally).
func TestModelStateFromACP_Populated(t *testing.T) {
	desc := "Fast tier model"
	src := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("m-1"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("m-1"), Name: "Model 1", Description: &desc},
			{ModelId: acp.ModelId("m-2"), Name: "Model 2"},
		},
	}

	got := ModelStateFromACP(src)
	if got == nil {
		t.Fatalf("expected non-nil result for populated src")
	}
	if got.CurrentModelId != "m-1" {
		t.Fatalf("CurrentModelId=%q, want %q", got.CurrentModelId, "m-1")
	}
	if len(got.AvailableModels) != 2 {
		t.Fatalf("len(AvailableModels)=%d, want 2", len(got.AvailableModels))
	}
	if got.AvailableModels[0].ModelId != "m-1" || got.AvailableModels[0].Name != "Model 1" {
		t.Fatalf("AvailableModels[0]=%+v", got.AvailableModels[0])
	}
	if got.AvailableModels[0].Description == nil || *got.AvailableModels[0].Description != desc {
		t.Fatalf("Description not preserved: %+v", got.AvailableModels[0].Description)
	}
	if got.AvailableModels[1].ModelId != "m-2" || got.AvailableModels[1].Description != nil {
		t.Fatalf("AvailableModels[1]=%+v", got.AvailableModels[1])
	}
}

// TestModelStateFromACP_PreservesDescriptionPointerIdentity documents that the
// converter aliases the SDK's Description *string rather than deep-copying it.
// If a future refactor decides to deep-copy (e.g. for cache safety), delete
// this test — it exists to make that behavioural choice explicit.
func TestModelStateFromACP_PreservesDescriptionPointerIdentity(t *testing.T) {
	desc := "shared"
	src := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("m-1"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("m-1"), Name: "M1", Description: &desc},
		},
	}
	got := ModelStateFromACP(src)
	if got.AvailableModels[0].Description != &desc {
		t.Fatalf("expected Description pointer aliased from SDK input")
	}
}

// TestModelStateFromACP_FeedsDownstreamPipeline is the mitto-i8n end-to-end
// unit-level check: a state produced by ModelStateFromACP (as if handed off
// from the resp.Models fallback in bgsession_acp_process.go) round-trips
// through ModelsToConfigOptions into a slice of SessionConfigOptionValue
// entries — the same shape acpCallbackSink.setAgentModels feeds into
// cbReplaceModelConfigOption for the frontend model selector. If this
// passes, the sink flow covered by the existing acp_callback_sink_test
// suite fires identically for a configOptions-empty / resp.Models-populated
// agent (e.g. Auggie).
func TestModelStateFromACP_FeedsDownstreamPipeline(t *testing.T) {
	src := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("auggie-opus"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("auggie-opus"), Name: "Auggie (Opus)"},
			{ModelId: acp.ModelId("auggie-sonnet"), Name: "Auggie (Sonnet)"},
		},
	}
	models := ModelStateFromACP(src)
	if models == nil {
		t.Fatalf("converter returned nil for populated src")
	}

	opts := ModelsToConfigOptions(models)
	if len(opts) != 2 {
		t.Fatalf("len(opts)=%d, want 2", len(opts))
	}
	if opts[0].Value != "auggie-opus" || opts[0].Name != "Auggie (Opus)" {
		t.Fatalf("opts[0]=%+v", opts[0])
	}
	if opts[1].Value != "auggie-sonnet" || opts[1].Name != "Auggie (Sonnet)" {
		t.Fatalf("opts[1]=%+v", opts[1])
	}
}

// TestDeriveAgentModels_PrefersConfigOptions verifies branch 1 (spec-aligned)
// wins whenever the agent advertises the catalog via configOptions[category="model"],
// regardless of resp.Models or local profile content. This ensures a valid ACP
// v0.13.5-compliant agent is never bypassed for the fallback branches.
func TestDeriveAgentModels_PrefersConfigOptions(t *testing.T) {
	modelCategory := acp.SessionConfigOptionCategoryModel
	opts := []acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:           acp.SessionConfigId("agent-model-id"),
			Name:         "Model",
			Category:     &modelCategory,
			CurrentValue: acp.SessionConfigValueId("cfg-m1"),
			Options: acp.SessionConfigSelectOptions{Ungrouped: &acp.SessionConfigSelectOptionsUngrouped{
				{Name: "CfgModel1", Value: acp.SessionConfigValueId("cfg-m1")},
			}},
		}},
	}
	respModels := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("acp-m1"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("acp-m1"), Name: "ACPModel1"},
		},
	}
	profiles := []config.ModelProfile{{Name: "ProfileA"}}

	models, cfgId, source := DeriveAgentModels(opts, respModels, profiles)
	if models == nil {
		t.Fatalf("expected non-nil models when configOptions has model category")
	}
	if source != ModelCatalogSourceConfigOptions {
		t.Fatalf("source=%q, want %q", source, ModelCatalogSourceConfigOptions)
	}
	if cfgId != acp.SessionConfigId("agent-model-id") {
		t.Fatalf("cfgId=%q, want %q", cfgId, "agent-model-id")
	}
	if models.CurrentModelId != "cfg-m1" {
		t.Fatalf("CurrentModelId=%q, want cfg-m1 (from configOptions, not resp.Models)", models.CurrentModelId)
	}
}

// TestDeriveAgentModels_FallsBackToACPModels covers the mitto-i8n scenario:
// configOptions is empty (or has no model category) but resp.Models is
// populated. The out-of-spec top-level path must win over local profiles.
func TestDeriveAgentModels_FallsBackToACPModels(t *testing.T) {
	respModels := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("acp-m1"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("acp-m1"), Name: "ACPModel1"},
		},
	}
	profiles := []config.ModelProfile{{Name: "ProfileA"}}

	models, cfgId, source := DeriveAgentModels(nil, respModels, profiles)
	if models == nil {
		t.Fatalf("expected non-nil models from resp.Models fallback")
	}
	if source != ModelCatalogSourceACPModels {
		t.Fatalf("source=%q, want %q", source, ModelCatalogSourceACPModels)
	}
	if cfgId != "" {
		t.Fatalf("cfgId=%q, want empty for acp_models branch", cfgId)
	}
	if models.CurrentModelId != "acp-m1" {
		t.Fatalf("CurrentModelId=%q, want acp-m1", models.CurrentModelId)
	}
	if len(models.AvailableModels) != 1 || models.AvailableModels[0].ModelId != "acp-m1" {
		t.Fatalf("AvailableModels=%+v", models.AvailableModels)
	}
}

// TestDeriveAgentModels_FallsBackToLocalProfiles is the mitto-886 core: when
// both spec-aligned and out-of-spec agent-side paths yield nil, synthesize
// from the caller's local profiles. cfgId must be empty so callers don't
// overwrite the conventional ModelConfigId default.
func TestDeriveAgentModels_FallsBackToLocalProfiles(t *testing.T) {
	profiles := []config.ModelProfile{
		{Name: "Opus"},
		{Name: "Sonnet"},
	}

	models, cfgId, source := DeriveAgentModels(nil, nil, profiles)
	if models == nil {
		t.Fatalf("expected non-nil models from local profile synthesis")
	}
	if source != ModelCatalogSourceLocalProfileFallback {
		t.Fatalf("source=%q, want %q", source, ModelCatalogSourceLocalProfileFallback)
	}
	if cfgId != "" {
		t.Fatalf("cfgId=%q, want empty for local_profile_fallback branch", cfgId)
	}
	if models.CurrentModelId != "" {
		t.Fatalf("CurrentModelId=%q, want empty (synth cannot know current)", models.CurrentModelId)
	}
	if len(models.AvailableModels) != 2 {
		t.Fatalf("len(AvailableModels)=%d, want 2", len(models.AvailableModels))
	}
	if models.AvailableModels[0].ModelId != "Opus" || models.AvailableModels[0].Name != "Opus" {
		t.Fatalf("AvailableModels[0]=%+v", models.AvailableModels[0])
	}
}

// TestDeriveAgentModels_NilWhenAllEmpty documents the terminal case: agent
// advertises no catalog via either path AND no local profiles are configured.
// Returns (nil, "", "") so LogSessionConfigOptions receives sourceFallback=""
// and preserves the pre-mitto-886 log format.
func TestDeriveAgentModels_NilWhenAllEmpty(t *testing.T) {
	models, cfgId, source := DeriveAgentModels(nil, nil, nil)
	if models != nil {
		t.Fatalf("expected nil models, got %+v", models)
	}
	if cfgId != "" {
		t.Fatalf("cfgId=%q, want empty", cfgId)
	}
	if source != "" {
		t.Fatalf("source=%q, want empty", source)
	}
}

// TestDeriveAgentModels_ConfigOptionsWithNonModelCategory_FallsThrough
// ensures ModelStateFromConfigOptions returning nil (because the only opts
// present are mode/reasoning selectors) is properly detected as "branch 1
// yielded nothing" so branch 2 gets a chance.
func TestDeriveAgentModels_ConfigOptionsWithNonModelCategory_FallsThrough(t *testing.T) {
	modeCategory := acp.SessionConfigOptionCategoryMode
	opts := []acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:           acp.SessionConfigId("session-mode"),
			Name:         "Mode",
			Category:     &modeCategory,
			CurrentValue: acp.SessionConfigValueId("plan"),
			Options: acp.SessionConfigSelectOptions{Ungrouped: &acp.SessionConfigSelectOptionsUngrouped{
				{Name: "Plan", Value: acp.SessionConfigValueId("plan")},
			}},
		}},
	}
	respModels := &acp.SessionModelState{
		CurrentModelId: acp.ModelId("acp-m1"),
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("acp-m1"), Name: "ACPModel1"},
		},
	}

	models, _, source := DeriveAgentModels(opts, respModels, nil)
	if models == nil {
		t.Fatalf("expected fallthrough to resp.Models when configOptions has no model category")
	}
	if source != ModelCatalogSourceACPModels {
		t.Fatalf("source=%q, want %q", source, ModelCatalogSourceACPModels)
	}
}

// TestLogSessionConfigOptions_SourceFallbackEmitted verifies the new
// "source_fallback" log field appears only when a non-empty tag is supplied
// (i.e. a non-primary branch of DeriveAgentModels won). This is the observable
// signal the mitto-886 rollout guide asks operators to grep for when
// diagnosing "empty selector" reports.
func TestLogSessionConfigOptions_SourceFallbackEmitted(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	LogSessionConfigOptions(logger, "session/new", nil, ModelCatalogSourceLocalProfileFallback)

	out := buf.String()
	if !strings.Contains(out, "source_fallback=local_profile_fallback") {
		t.Fatalf("expected source_fallback=local_profile_fallback in log output, got:\n%s", out)
	}
	if !strings.Contains(out, "source=session/new") {
		t.Fatalf("expected source=session/new in log output, got:\n%s", out)
	}
	if !strings.Contains(out, "has_model_option=false") {
		t.Fatalf("expected has_model_option=false in log output, got:\n%s", out)
	}
}

// TestLogSessionConfigOptions_SourceFallbackOmittedWhenEmpty is the "byte-
// identical to pre-mitto-886" guarantee for the primary-branch path: when
// configOptions wins, sourceFallback is "" and the log line must not carry
// the new field at all (so grep-based dashboards that key on its absence
// don't have to change).
func TestLogSessionConfigOptions_SourceFallbackOmittedWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	modelCategory := acp.SessionConfigOptionCategoryModel
	opts := []acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:           acp.SessionConfigId("model"),
			Name:         "Model",
			Category:     &modelCategory,
			CurrentValue: acp.SessionConfigValueId("m1"),
			Options: acp.SessionConfigSelectOptions{Ungrouped: &acp.SessionConfigSelectOptionsUngrouped{
				{Name: "M1", Value: acp.SessionConfigValueId("m1")},
			}},
		}},
	}

	LogSessionConfigOptions(logger, "session/new", opts, "")

	out := buf.String()
	if strings.Contains(out, "source_fallback") {
		t.Fatalf("expected NO source_fallback field for empty tag, got:\n%s", out)
	}
	if !strings.Contains(out, "has_model_option=true") {
		t.Fatalf("expected has_model_option=true, got:\n%s", out)
	}
	if !strings.Contains(out, "config_option_count=1") {
		t.Fatalf("expected config_option_count=1, got:\n%s", out)
	}
}

// TestLogSessionConfigOptions_NilLoggerNoOp guards against a nil-logger panic
// so callers on the shared-process path (which may run before per-session
// loggers are wired) can call this unconditionally.
func TestLogSessionConfigOptions_NilLoggerNoOp(t *testing.T) {
	// Simply must not panic.
	LogSessionConfigOptions(nil, "session/new", nil, ModelCatalogSourceACPModels)
}
