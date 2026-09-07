package acpbackend

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// ToNeutralModelState translates Mitto's already-derived
// conversation.SessionModelState (see conversation.DeriveAgentModels) into
// the neutral ModelState. Returns nil for a nil input.
func ToNeutralModelState(s *conversation.SessionModelState) *agentbackend.ModelState {
	if s == nil {
		return nil
	}
	out := &agentbackend.ModelState{
		CurrentModelID: s.CurrentModelId,
		Available:      make([]agentbackend.ModelDescriptor, 0, len(s.AvailableModels)),
	}
	for _, m := range s.AvailableModels {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		out.Available = append(out.Available, agentbackend.ModelDescriptor{
			ID:          m.ModelId,
			Name:        m.Name,
			Description: desc,
		})
	}
	return out
}

// ToNeutralModeState translates a raw ACP SessionModeState into the neutral
// ModeState. Returns nil for a nil input.
func ToNeutralModeState(s *acp.SessionModeState) *agentbackend.ModeState {
	if s == nil {
		return nil
	}
	out := &agentbackend.ModeState{
		CurrentModeID: string(s.CurrentModeId),
		Available:     make([]agentbackend.ModeDescriptor, 0, len(s.AvailableModes)),
	}
	for _, m := range s.AvailableModes {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		out.Available = append(out.Available, agentbackend.ModeDescriptor{
			ID:          string(m.Id),
			Name:        m.Name,
			Description: desc,
		})
	}
	return out
}

// ToNeutralConfigOption translates Mitto's already-normalized
// conversation.SessionConfigOption (which itself already flattens the ACP
// Select/Boolean config-option union) into the neutral ConfigOption.
func ToNeutralConfigOption(o conversation.SessionConfigOption) agentbackend.ConfigOption {
	values := make([]agentbackend.ConfigOptionValue, 0, len(o.Options))
	for _, v := range o.Options {
		values = append(values, agentbackend.ConfigOptionValue{
			Value:       v.Value,
			Name:        v.Name,
			Description: v.Description,
		})
	}
	return agentbackend.ConfigOption{
		ID:       o.ID,
		Category: o.Category,
		Current:  o.CurrentValue,
		Values:   values,
	}
}

// ToNeutralConfigOptions translates a slice of conversation.SessionConfigOption.
func ToNeutralConfigOptions(opts []conversation.SessionConfigOption) []agentbackend.ConfigOption {
	out := make([]agentbackend.ConfigOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, ToNeutralConfigOption(o))
	}
	return out
}
