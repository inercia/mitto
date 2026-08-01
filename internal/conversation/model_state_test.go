package conversation

import (
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

// unused import guard: config is imported to keep goimports stable if this
// file is later extended with profile-driven tests (SynthesizeModelStateFromProfiles
// lives in the same file as ModelStateFromACP).
var _ = config.ModelProfile{}
