package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// TestHandleWorkspacePromptsGET_IncludeGlobal_SurfacesLoadError is the
// reproduction test for mitto-tigh (gap 2 + investigation Finding B): a
// workspace prompt file that fails to parse must still be visible in the
// workspace-prompts API response, mirroring how workspace_processors.go
// surfaces a ProcessorLoadError via a placeholder WebProcessor{Error: ...}
// entry (see workspace_processors.go:139-158).
//
// Today HandleWorkspacePromptsGETIncludeGlobal (workspace_prompts.go:195 and
// :206) calls the two-value configPkg.LoadPromptsFromDir wrapper, which
// silently discards the per-file error slice. The broken prompt simply never
// appears in the "prompts" list, and there is no error indication anywhere
// in the JSON response — worse than "toast-only" (gap 2 in the bead), the
// include_global path never even reaches PromptsCache.LoadErrors() to
// broadcast a toast in the first place.
func TestHandleWorkspacePromptsGET_IncludeGlobal_SurfacesLoadError(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	wsDir := t.TempDir()
	promptsDir := appdir.WorkspacePromptsDir(wsDir)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// One valid prompt.
	good := "name: \"Good\"\nprompt: |\n  ok.\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "good.prompt.yaml"), []byte(good), 0644); err != nil {
		t.Fatalf("WriteFile good: %v", err)
	}
	// One prompt file that fails to parse (malformed YAML: unclosed flow sequence).
	bad := "name: [unclosed\nprompt: |\n  broken.\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "bad.prompt.yaml"), []byte(bad), 0644); err != nil {
		t.Fatalf("WriteFile bad: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "ws-uuid", WorkingDir: wsDir}})
	h := New(Deps{SessionManager: sm})

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts?working_dir="+wsDir+"&include_global=true", nil)
	w := httptest.NewRecorder()
	h.HandleWorkspacePromptsGET(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Prompts []map[string]interface{} `json:"prompts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}

	// The broken prompt file must be surfaced somewhere in the response with
	// a non-empty error, mirroring workspace_processors.go's
	// ProcessorLoadError placeholder pattern. BUG (mitto-tigh): it is
	// silently dropped instead — only the good prompt comes back, with no
	// trace of bad.prompt.yaml anywhere in the response.
	foundError := false
	for _, p := range body.Prompts {
		if errStr, ok := p["error"].(string); ok && errStr != "" {
			foundError = true
		}
	}
	if !foundError {
		t.Errorf("expected an entry with a non-empty \"error\" field for the malformed "+
			"bad.prompt.yaml (mitto-tigh gap 2 / Finding B: HandleWorkspacePromptsGETIncludeGlobal "+
			"uses LoadPromptsFromDir, which drops load errors); got %d prompt(s), none carrying "+
			"an error: %+v", len(body.Prompts), body.Prompts)
	}
}
