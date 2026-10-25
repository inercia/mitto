package conversation

import (
	"fmt"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// resolveInitialModelPreference prepends a named prompt's preferences to the
// defaults. Unmatched prompt preferences still fall back to those defaults.
func (sm *SessionManager) resolveInitialModelPreference(promptName, workingDir string, fallback []config.PromptPreferredModel) []config.PromptPreferredModel {
	if promptName == "" {
		return fallback
	}
	sm.mu.RLock()
	resolver := sm.preferredModelsResolver
	sm.mu.RUnlock()
	if resolver == nil {
		return fallback
	}
	prefs := resolver(promptName, workingDir)
	if len(prefs) == 0 {
		return fallback
	}
	merged := make([]config.PromptPreferredModel, 0, len(prefs)+len(fallback))
	merged = append(merged, prefs...)
	merged = append(merged, fallback...)
	return merged
}

// defaultInitialModelPreference owns the workspace -> ACP-server default rule
// for all creation paths. A workspace setting replaces the server default;
// prompt/explicit preferences are prepended by the caller. Concrete selection,
// eligibility and workspace tag overrides belong to cbInitBaselineModelIfEmpty.
func defaultInitialModelPreference(ws *config.WorkspaceSettings, server string, cfg *config.Config) []config.PromptPreferredModel {
	if pref := ws.GetInitialModelPreference(); len(pref) != 0 {
		return pref
	}
	if cfg != nil {
		if srv, err := cfg.GetServer(server); err == nil {
			return srv.GetInitialModelPreference()
		}
	}
	return nil
}

// prepareInitialModelPreference is shared by all persisted-session entry points.
// A real resume keeps its baseline. A not-yet-started conversation retains its
// initial preference chain across failures, including plain ResumeSession retries.
func (sm *SessionManager) prepareInitialModelPreference(id, workingDir string, ws *config.WorkspaceSettings, server string, explicit []config.PromptPreferredModel) ([]config.PromptPreferredModel, error) {
	fallback := defaultInitialModelPreference(ws, server, sm.mittoConfig)
	if sm.store == nil {
		return append(append([]config.PromptPreferredModel(nil), explicit...), fallback...), nil
	}
	meta, err := sm.store.GetMetadata(id)
	if err != nil {
		return nil, fmt.Errorf("load initial model preference: %w", err)
	}
	if meta.BaselineModel != "" || meta.PendingModelMappingFrom != "" {
		return nil, nil
	}
	if len(meta.PendingInitialModelPreference) != 0 {
		return meta.PendingInitialModelPreference, nil
	}
	prefs := append(append([]config.PromptPreferredModel(nil), explicit...), fallback...)
	if len(explicit) == 0 {
		prefs = sm.resolveInitialModelPreference(meta.OriginPromptName, workingDir, fallback)
	}
	if len(prefs) != 0 {
		if err := sm.store.UpdateMetadata(id, func(m *session.Metadata) {
			if m.BaselineModel == "" {
				m.PendingInitialModelPreference = prefs
			}
		}); err != nil {
			return nil, fmt.Errorf("persist initial model preference: %w", err)
		}
	}
	return prefs, nil
}
