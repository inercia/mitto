package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// newProcHandlers builds a Handlers facade for the workspace processors tests,
// wiring only the dependency the processors handlers use.
func newProcHandlers(sm *conversation.SessionManager) *Handlers {
	return New(Deps{SessionManager: sm})
}

// TestToggleEnabled_SingleDocFile verifies that toggling a processor whose YAML
// file contains a single document updates the file in-place (existing behavior).
func TestToggleEnabled_SingleDocFile(t *testing.T) {
	wsDir := t.TempDir()

	// Create the workspace processors directory and a single-doc processor file.
	procDir := filepath.Join(wsDir, ".mitto", "processors")
	if err := os.MkdirAll(procDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	procFile := filepath.Join(procDir, "my-proc.yaml")
	original := "name: my-proc\nwhen:\n  on: userPrompt\n  match: all\ncommand: /bin/echo\n"
	if err := os.WriteFile(procFile, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	h := newProcHandlers(sm)

	body, _ := json.Marshal(map[string]interface{}{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-uuid/processors/my-proc", bytes.NewReader(body))
	req.SetPathValue("uuid", "ws-uuid")
	req.SetPathValue("name", "my-proc")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWorkspaceProcessorPatch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// The processor file must have been updated in-place.
	data, err := os.ReadFile(procFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("expected 'enabled: false' in file after toggle; got:\n%s", string(data))
	}

	// No .mittorc should have been created (in-place path, not .mittorc path).
	rcPath := filepath.Join(wsDir, ".mittorc")
	if _, err := os.Stat(rcPath); err == nil {
		data, _ := os.ReadFile(rcPath)
		t.Errorf(".mittorc should NOT be created for single-doc toggle; content:\n%s", string(data))
	}
}

// TestToggleEnabled_MultiDocFile verifies that toggling a processor whose YAML
// file contains multiple `---`-separated documents writes to .mittorc and leaves
// the YAML file byte-identical to the original.
func TestToggleEnabled_MultiDocFile(t *testing.T) {
	wsDir := t.TempDir()

	// Create the workspace processors directory and a multi-doc processor file.
	procDir := filepath.Join(wsDir, ".mitto", "processors")
	if err := os.MkdirAll(procDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	procFile := filepath.Join(procDir, "multi-proc.yaml")
	original := "name: multi-proc\nwhen:\n  on: userPrompt\n  match: all\ncommand: /bin/echo\n---\nname: multi-proc-b\nwhen:\n  on: agentResponded\n  match: all\ncommand: /bin/echo\n"
	if err := os.WriteFile(procFile, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	h := newProcHandlers(sm)

	body, _ := json.Marshal(map[string]interface{}{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-uuid/processors/multi-proc", bytes.NewReader(body))
	req.SetPathValue("uuid", "ws-uuid")
	req.SetPathValue("name", "multi-proc")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWorkspaceProcessorPatch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// The multi-doc file must be byte-identical to the original (not rewritten).
	data, err := os.ReadFile(procFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Errorf("multi-doc YAML file was modified:\ngot:\n%s\nwant:\n%s", string(data), original)
	}

	// .mittorc must have been created with the processors override.
	rcPath := filepath.Join(wsDir, ".mittorc")
	rcData, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf(".mittorc not created: %v", err)
	}
	if !strings.Contains(string(rcData), "multi-proc") {
		t.Errorf(".mittorc does not contain 'multi-proc':\n%s", string(rcData))
	}
	if !strings.Contains(string(rcData), "enabled: false") {
		t.Errorf(".mittorc does not contain 'enabled: false':\n%s", string(rcData))
	}
}

// TestToggleEnabled_GlobalProcessor verifies that toggling a global processor
// (not found in workspace dirs) writes to .mittorc.
func TestToggleEnabled_GlobalProcessor(t *testing.T) {
	wsDir := t.TempDir()
	// Do NOT create any processor file in the workspace dir —
	// simulates a global/builtin processor.

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	h := newProcHandlers(sm)

	body, _ := json.Marshal(map[string]interface{}{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-uuid/processors/global-proc", bytes.NewReader(body))
	req.SetPathValue("uuid", "ws-uuid")
	req.SetPathValue("name", "global-proc")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWorkspaceProcessorPatch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// .mittorc must record the override.
	rcPath := filepath.Join(wsDir, ".mittorc")
	rcData, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf(".mittorc not created: %v", err)
	}
	if !strings.Contains(string(rcData), "global-proc") {
		t.Errorf(".mittorc does not contain 'global-proc':\n%s", string(rcData))
	}
}
