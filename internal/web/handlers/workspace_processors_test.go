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
	"github.com/inercia/mitto/internal/processors"
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

// --- helpers for parameter tests ---

// setupPromptProc writes a prompt-mode processor YAML with parameters to the workspace's
// .mitto/processors/ directory and returns the workspace dir.
// It also injects an empty processor manager so that GetWorkspaceProcessorManager
// loads workspace-local processors from the .mitto/processors/ directory.
func setupPromptProc(t *testing.T, name, yaml string) (wsDir string, sm *conversation.SessionManager) {
	t.Helper()
	wsDir = t.TempDir()
	procDir := filepath.Join(wsDir, ".mitto", "processors")
	if err := os.MkdirAll(procDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(procDir, name+".yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sm = conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	// An empty (non-nil) processor manager is needed so that GetWorkspaceProcessorManager
	// descends into loadWorkspaceProcessors and loads the workspace-local YAML files.
	sm.SetProcessorManager(processors.NewManager("", nil))
	return wsDir, sm
}

// doGET fires a GET /api/workspaces/ws-uuid/processors request and decodes the body.
func doGET(t *testing.T, h *Handlers) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-uuid/processors", nil)
	req.SetPathValue("uuid", "ws-uuid")
	w := httptest.NewRecorder()
	h.HandleWorkspaceProcessors(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal GET body: %v", err)
	}
	return body
}

// firstProcessor returns the first processor entry from a GET response body,
// failing the test if there are none.
func firstProcessor(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	list, _ := body["processors"].([]interface{})
	if len(list) == 0 {
		t.Fatal("expected at least one processor in GET response")
	}
	p, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("processor entry is not a map: %T", list[0])
	}
	return p
}

// --- GET parameters tests ---

// TestGetProcessors_ParametersIncluded verifies that a prompt-mode processor with
// declared parameters surfaces them in the GET response with default values.
func TestGetProcessors_ParametersIncluded(t *testing.T) {
	yaml := `
name: manage-rules
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
    description: Target file
    default: AGENTS.md
`
	_, sm := setupPromptProc(t, "manage-rules", yaml)
	h := newProcHandlers(sm)

	body := doGET(t, h)
	proc := firstProcessor(t, body)

	if proc["mode"] != "prompt" {
		t.Errorf("mode = %v, want prompt", proc["mode"])
	}

	rawParams, _ := proc["parameters"].([]interface{})
	if len(rawParams) != 1 {
		t.Fatalf("parameters count = %d, want 1; full proc: %v", len(rawParams), proc)
	}
	param, _ := rawParams[0].(map[string]interface{})
	if param["name"] != "filename" {
		t.Errorf("param name = %v, want filename", param["name"])
	}
	if param["default"] != "AGENTS.md" {
		t.Errorf("param default = %v, want AGENTS.md", param["default"])
	}
	// No workspace override yet → value == default
	if param["value"] != "AGENTS.md" {
		t.Errorf("param value = %v, want AGENTS.md (default)", param["value"])
	}
}

// TestGetProcessors_EffectiveValueFromOverride verifies that when a per-workspace
// argument is saved in .mittorc, the GET response reflects the override in "value".
func TestGetProcessors_EffectiveValueFromOverride(t *testing.T) {
	yaml := `
name: manage-rules
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
    default: AGENTS.md
`
	wsDir, sm := setupPromptProc(t, "manage-rules", yaml)
	h := newProcHandlers(sm)

	// Pre-seed a .mittorc override
	if err := config.SaveWorkspaceRCProcessorArguments(wsDir, "manage-rules", map[string]string{
		"filename": "CONTRIBUTORS.md",
	}); err != nil {
		t.Fatalf("SaveWorkspaceRCProcessorArguments: %v", err)
	}

	body := doGET(t, h)
	proc := firstProcessor(t, body)

	rawParams, _ := proc["parameters"].([]interface{})
	if len(rawParams) != 1 {
		t.Fatalf("parameters count = %d, want 1", len(rawParams))
	}
	param, _ := rawParams[0].(map[string]interface{})
	// default stays as declared
	if param["default"] != "AGENTS.md" {
		t.Errorf("param default = %v, want AGENTS.md", param["default"])
	}
	// value must reflect the workspace override
	if param["value"] != "CONTRIBUTORS.md" {
		t.Errorf("param value = %v, want CONTRIBUTORS.md (override)", param["value"])
	}
}

// --- PUT /arguments tests ---

// doPUT fires a PUT /arguments request and returns the recorder.
func doPUT(t *testing.T, h *Handlers, procName string, args map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"arguments": args})
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/ws-uuid/processors/"+procName+"/arguments", bytes.NewReader(body))
	req.SetPathValue("uuid", "ws-uuid")
	req.SetPathValue("name", procName)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleWorkspaceProcessorArguments(w, req)
	return w
}

// TestSaveArguments_RoundTrip verifies that saving arguments persists to .mittorc
// and that a subsequent GET reflects the updated effective values.
func TestSaveArguments_RoundTrip(t *testing.T) {
	yaml := `
name: manage-rules
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
    default: AGENTS.md
`
	_, sm := setupPromptProc(t, "manage-rules", yaml)
	h := newProcHandlers(sm)

	// Save the argument override.
	w := doPUT(t, h, "manage-rules", map[string]interface{}{"filename": "CONTRIBUTORS.md"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Decode the PUT response — must contain updated effective value.
	var putResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("unmarshal PUT response: %v", err)
	}
	putParams, _ := putResp["parameters"].([]interface{})
	if len(putParams) != 1 {
		t.Fatalf("PUT response parameters count = %d, want 1", len(putParams))
	}
	putParam, _ := putParams[0].(map[string]interface{})
	if putParam["value"] != "CONTRIBUTORS.md" {
		t.Errorf("PUT response param value = %v, want CONTRIBUTORS.md", putParam["value"])
	}

	// Verify the subsequent GET reflects the override too.
	body := doGET(t, h)
	proc := firstProcessor(t, body)
	rawParams, _ := proc["parameters"].([]interface{})
	if len(rawParams) != 1 {
		t.Fatalf("GET parameters count = %d, want 1", len(rawParams))
	}
	getParam, _ := rawParams[0].(map[string]interface{})
	if getParam["value"] != "CONTRIBUTORS.md" {
		t.Errorf("GET param value after save = %v, want CONTRIBUTORS.md", getParam["value"])
	}
}

// TestSaveArguments_UnknownParamRejected verifies that an unknown parameter name
// is rejected with a 400 error envelope.
func TestSaveArguments_UnknownParamRejected(t *testing.T) {
	yaml := `
name: manage-rules
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
    default: AGENTS.md
`
	_, sm := setupPromptProc(t, "manage-rules", yaml)
	h := newProcHandlers(sm)

	w := doPUT(t, h, "manage-rules", map[string]interface{}{"nonexistent": "value"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	// Must be canonical error envelope.
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	errObj, _ := env["error"].(map[string]interface{})
	if errObj["code"] != "bad_request" {
		t.Errorf("error.code = %v, want bad_request", errObj["code"])
	}
	if !strings.Contains(errObj["message"].(string), "nonexistent") {
		t.Errorf("error.message should mention the unknown parameter; got: %v", errObj["message"])
	}
}

// TestSaveArguments_NonPromptModeRejected verifies that trying to save arguments
// for a command-mode processor returns a 400 error.
func TestSaveArguments_NonPromptModeRejected(t *testing.T) {
	yaml := `
name: cmd-proc
when:
  on: userPrompt
  match: all
command: /bin/echo
`
	_, sm := setupPromptProc(t, "cmd-proc", yaml)
	h := newProcHandlers(sm)

	w := doPUT(t, h, "cmd-proc", map[string]interface{}{"any": "value"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	errObj, _ := env["error"].(map[string]interface{})
	if errObj["code"] != "bad_request" {
		t.Errorf("error.code = %v, want bad_request", errObj["code"])
	}
}

// TestSaveArguments_UnknownProcessorRejected verifies that saving arguments for a
// non-existent processor returns a 404 error envelope.
func TestSaveArguments_UnknownProcessorRejected(t *testing.T) {
	wsDir := t.TempDir()
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	h := newProcHandlers(sm)

	w := doPUT(t, h, "ghost-proc", map[string]interface{}{"x": "y"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	errObj, _ := env["error"].(map[string]interface{})
	if errObj["code"] != "not_found" {
		t.Errorf("error.code = %v, want not_found", errObj["code"])
	}
}

// TestSaveArguments_EmptyValueClearsOverride verifies that passing an empty string
// for a parameter key removes the workspace override, reverting to the default.
func TestSaveArguments_EmptyValueClearsOverride(t *testing.T) {
	yaml := `
name: manage-rules
when:
  on: userPrompt
  match: first
prompt: "Save to ${filename}."
parameters:
  - name: filename
    type: text
    default: AGENTS.md
`
	wsDir, sm := setupPromptProc(t, "manage-rules", yaml)
	h := newProcHandlers(sm)

	// First, set an override.
	w := doPUT(t, h, "manage-rules", map[string]interface{}{"filename": "CONTRIBUTORS.md"})
	if w.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d; body: %s", w.Code, w.Body.String())
	}

	// Verify override was set.
	rcData, _ := os.ReadFile(filepath.Join(wsDir, ".mittorc"))
	if !strings.Contains(string(rcData), "CONTRIBUTORS.md") {
		t.Errorf(".mittorc should contain override after PUT; got:\n%s", string(rcData))
	}

	// Clear the override by sending empty string.
	w2 := doPUT(t, h, "manage-rules", map[string]interface{}{"filename": ""})
	if w2.Code != http.StatusOK {
		t.Fatalf("clear PUT status = %d; body: %s", w2.Code, w2.Body.String())
	}

	// The effective value must have reverted to the default.
	var resp map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal clear response: %v", err)
	}
	params, _ := resp["parameters"].([]interface{})
	if len(params) == 0 {
		t.Fatal("expected parameters in clear response")
	}
	p, _ := params[0].(map[string]interface{})
	if p["value"] != "AGENTS.md" {
		t.Errorf("value after clear = %v, want AGENTS.md (default)", p["value"])
	}
}

