package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// newGlobalShortcutsHandlers wires a Handlers facade with an isolated MITTO_DIR
// and an empty MittoConfig (settings prompts). BuiltinPromptsDir() may resolve
// to an embedded/dev-tree location; we only assert on payload shape, not on
// prompt-list contents, to keep the test hermetic.
func newGlobalShortcutsHandlers(t *testing.T) *Handlers {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return New(Deps{MittoConfig: &config.Config{}})
}

// TestHandleGlobalShortcutsGet_OmitsPromptsByDefault verifies that the hot-path
// callers (conversation toolbar, tasks-list toolbar, beads panel chrome, folder
// shortcuts tab) receive a lean response without the ~750 KB merged prompts
// list. See mitto-r4t0.
func TestHandleGlobalShortcutsGet_OmitsPromptsByDefault(t *testing.T) {
	h := newGlobalShortcutsHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/global/shortcuts", nil)
	w := httptest.NewRecorder()
	h.HandleGlobalShortcuts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// Decode into a struct that preserves whether "prompts" was present in JSON
	// (a nil slice + omitempty means the key is not emitted).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if _, ok := raw["sections"]; !ok {
		t.Errorf("response missing 'sections' key: %s", w.Body.String())
	}
	if _, ok := raw["prompts"]; ok {
		t.Errorf("response should NOT include 'prompts' when include_prompts is unset: %s", w.Body.String())
	}
}

// TestHandleGlobalShortcutsGet_IncludesPromptsWhenRequested verifies that the
// shortcuts editor (SettingsDialog.js) still receives the merged prompt list
// when it passes ?include_prompts=true. Seeds a settings-level prompt so the
// resulting Prompts slice is non-empty (omitempty would otherwise elide the
// key even under the include_prompts=true branch — see globalPrompts()).
func TestHandleGlobalShortcutsGet_IncludesPromptsWhenRequested(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	h := New(Deps{MittoConfig: &config.Config{
		Prompts: []config.WebPrompt{
			{Name: "seed", Prompt: "hello", Source: config.PromptSourceSettings},
		},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/global/shortcuts?include_prompts=true", nil)
	w := httptest.NewRecorder()
	h.HandleGlobalShortcuts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if _, ok := raw["sections"]; !ok {
		t.Errorf("response missing 'sections' key: %s", w.Body.String())
	}
	if _, ok := raw["prompts"]; !ok {
		t.Errorf("response should include 'prompts' when include_prompts=true: %s", w.Body.String())
	}
}

// TestHandleGlobalShortcutsGet_IgnoresNonTrueValues verifies the gate is
// explicit-opt-in (any value other than the literal string "true" keeps the
// lean default response).
func TestHandleGlobalShortcutsGet_IgnoresNonTrueValues(t *testing.T) {
	h := newGlobalShortcutsHandlers(t)
	for _, v := range []string{"1", "yes", "TRUE", "false", ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/global/shortcuts?include_prompts="+v, nil)
		w := httptest.NewRecorder()
		h.HandleGlobalShortcuts(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("value=%q: status = %d, want 200", v, w.Code)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("value=%q: decode: %v", v, err)
		}
		if _, ok := raw["prompts"]; ok {
			t.Errorf("value=%q: response should NOT include 'prompts'", v)
		}
	}
}
