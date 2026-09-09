package acpbackend

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/conversation"
)

func TestToNeutralModelState(t *testing.T) {
	if got := ToNeutralModelState(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}

	desc := "fast and cheap"
	in := &conversation.SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []conversation.ModelInfo{
			{ModelId: "m-1", Name: "Model 1", Description: &desc},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	out := ToNeutralModelState(in)
	if out.CurrentModelID != "m-1" {
		t.Errorf("CurrentModelID = %q, want m-1", out.CurrentModelID)
	}
	if len(out.Available) != 2 {
		t.Fatalf("Available len = %d, want 2", len(out.Available))
	}
	if out.Available[0].Description != desc {
		t.Errorf("Description = %q, want %q", out.Available[0].Description, desc)
	}
	if out.Available[1].Description != "" {
		t.Errorf("Description = %q, want empty for nil pointer", out.Available[1].Description)
	}
}

func TestToNeutralModeState(t *testing.T) {
	if got := ToNeutralModeState(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}

	desc := "code editing mode"
	in := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "code", Name: "Code", Description: &desc},
			{Id: "chat", Name: "Chat"},
		},
	}
	out := ToNeutralModeState(in)
	if out.CurrentModeID != "code" {
		t.Errorf("CurrentModeID = %q, want code", out.CurrentModeID)
	}
	if len(out.Available) != 2 || out.Available[0].Description != desc || out.Available[1].Description != "" {
		t.Errorf("unexpected Available: %+v", out.Available)
	}
}

func TestToNeutralConfigOption(t *testing.T) {
	in := conversation.SessionConfigOption{
		ID:           "model",
		Category:     "model",
		CurrentValue: "m-1",
		Options: []conversation.SessionConfigOptionValue{
			{Value: "m-1", Name: "Model 1", Description: "d1"},
			{Value: "m-2", Name: "Model 2"},
		},
	}
	out := ToNeutralConfigOption(in)
	if out.ID != "model" || out.Category != "model" || out.Current != "m-1" {
		t.Errorf("unexpected header fields: %+v", out)
	}
	if len(out.Values) != 2 || out.Values[0].Description != "d1" {
		t.Errorf("unexpected values: %+v", out.Values)
	}
}

func TestToNeutralConfigOptions(t *testing.T) {
	in := []conversation.SessionConfigOption{
		{ID: "a"}, {ID: "b"},
	}
	out := ToNeutralConfigOptions(in)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "b" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if got := ToNeutralConfigOptions(nil); len(got) != 0 {
		t.Errorf("expected empty slice for nil input, got %+v", got)
	}
}
