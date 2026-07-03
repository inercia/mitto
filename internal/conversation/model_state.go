package conversation

import (
	acp "github.com/coder/acp-go-sdk"
)

// StableToUnstableModelState converts a *acp.SessionModelState (from NewSession/LoadSession)
// to *acp.UnstableSessionModelState so both stable and unstable model state responses
// can be stored in a unified field.
func StableToUnstableModelState(m *acp.SessionModelState) *acp.UnstableSessionModelState {
	if m == nil {
		return nil
	}
	models := make([]acp.UnstableModelInfo, len(m.AvailableModels))
	for i, mi := range m.AvailableModels {
		models[i] = acp.UnstableModelInfo{
			Meta:        mi.Meta,
			Description: mi.Description,
			ModelId:     acp.UnstableModelId(mi.ModelId),
			Name:        mi.Name,
		}
	}
	return &acp.UnstableSessionModelState{
		Meta:            m.Meta,
		AvailableModels: models,
		CurrentModelId:  acp.UnstableModelId(m.CurrentModelId),
	}
}
