package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
)

// TestResolveSuppressAutoChildrenByPromptName pins mitto-nlx: the resolver
// reads the target.suppressAutoChildren flag from the merged 5-tier prompt
// list (global file → settings inline → ACP-specific → workspace-dir →
// workspace-inline), matches by prompt name (case-insensitive per the
// existing resolver pattern), and returns false when the prompt is not
// found or does not declare the flag.
//
// Each subtest constructs a minimal *Server with a real PromptsCache scoped
// to a temp MITTO_DIR and a real SessionManager with a workspace fixed to
// workingDir. Prompts are injected at whichever tier the case exercises:
// prompt files in MITTO_DIR/prompts/ (global), MittoConfig.Prompts
// (settings), or a workspace .mittorc (workspace-inline).
func TestResolveSuppressAutoChildrenByPromptName(t *testing.T) {
	t.Run("prompt not found returns false", func(t *testing.T) {
		s := newSuppressResolverServer(t, nil, nil, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("no-such-prompt", "/work"); got {
			t.Errorf("resolveSuppressAutoChildrenByPromptName(missing) = true, want false")
		}
	})

	t.Run("global prompt with flag true returns true", func(t *testing.T) {
		files := map[string]string{
			"suppress.prompt.yaml": `name: "no-children"
target:
  suppressAutoChildren: true
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("no-children", "/work"); !got {
			t.Errorf("global prompt with SuppressAutoChildren=true: got false, want true")
		}
	})

	t.Run("global prompt without target returns false", func(t *testing.T) {
		files := map[string]string{
			"plain.prompt.yaml": `name: "plain"
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("plain", "/work"); got {
			t.Errorf("global prompt without Target: got true, want false")
		}
	})

	t.Run("global prompt with target but flag absent returns false", func(t *testing.T) {
		files := map[string]string{
			"titled.prompt.yaml": `name: "titled"
target:
  title: "Only a title"
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("titled", "/work"); got {
			t.Errorf("global prompt with target but no suppressAutoChildren: got true, want false")
		}
	})

	t.Run("settings prompt overrides global (settings=false wins over global=true)", func(t *testing.T) {
		files := map[string]string{
			"suppress.prompt.yaml": `name: "shared"
target:
  suppressAutoChildren: true
prompt: hi
`,
		}
		// Settings prompt with the SAME name but no suppressAutoChildren: the
		// higher-priority settings entry must win, so the resolver returns
		// false even though the global file says true.
		settings := []config.WebPrompt{
			{Name: "shared", Prompt: "settings body"},
		}
		s := newSuppressResolverServer(t, files, settings, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("shared", "/work"); got {
			t.Errorf("settings override of global suppress=true: got true, want false (higher-priority tier wins)")
		}
	})

	t.Run("settings prompt sets flag when global does not declare it", func(t *testing.T) {
		files := map[string]string{
			"plain.prompt.yaml": `name: "shared"
prompt: hi
`,
		}
		on := true
		settings := []config.WebPrompt{
			{
				Name:   "shared",
				Prompt: "settings body",
				Target: &prompts.PromptTarget{SuppressAutoChildren: on},
			},
		}
		s := newSuppressResolverServer(t, files, settings, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("shared", "/work"); !got {
			t.Errorf("settings SuppressAutoChildren=true over plain global: got false, want true")
		}
	})

	t.Run("workspace-inline prompt overrides settings and global", func(t *testing.T) {
		files := map[string]string{
			"suppress.prompt.yaml": `name: "shared"
target:
  suppressAutoChildren: true
prompt: hi
`,
		}
		settings := []config.WebPrompt{
			{
				Name:   "shared",
				Prompt: "settings body",
				Target: &prompts.PromptTarget{SuppressAutoChildren: true},
			},
		}
		// workspace-inline (.mittorc) explicitly sets suppressAutoChildren
		// to false; being the highest-priority tier it must win over both
		// the global file and the settings entry.
		mittorc := `prompts:
  - name: "shared"
    prompt: "workspace body"
    target:
      title: "explicit title"
`
		s := newSuppressResolverServer(t, files, settings, mittorc)
		if got := s.resolveSuppressAutoChildrenByPromptName("shared", s.sessionManager.GetDefaultWorkspace().WorkingDir); got {
			t.Errorf("workspace-inline override of settings suppress=true: got true, want false")
		}
	})

	t.Run("prompt name lookup is case-insensitive", func(t *testing.T) {
		files := map[string]string{
			"case.prompt.yaml": `name: "MixedCase"
target:
  suppressAutoChildren: true
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		if got := s.resolveSuppressAutoChildrenByPromptName("mixedcase", "/work"); !got {
			t.Errorf("case-insensitive lookup of \"mixedcase\" against \"MixedCase\": got false, want true")
		}
	})
}

// newSuppressResolverServer builds a minimal *Server wired for testing
// resolveSuppressAutoChildrenByPromptName. It creates a temp MITTO_DIR
// (so PromptsCache reads the provided global prompt files), attaches an
// optional settings-prompts list to MittoConfig, and optionally writes a
// workspace .mittorc for the workspace-inline tier. The returned server
// has sessionManager wired to a workspace at either mittorcDir (when
// mittorcContent is set) or /work.
func newSuppressResolverServer(t *testing.T, files map[string]string, settingsPrompts []config.WebPrompt, mittorcContent string) *Server {
	t.Helper()

	// Isolate MITTO_DIR so PromptsCache reads only the files we plant.
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	if len(files) > 0 {
		promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatalf("mkdir prompts dir: %v", err)
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(promptsDir, name), []byte(body), 0644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}

	// Workspace dir: when a .mittorc is provided, plant it in a fresh temp
	// dir and use that as the workspace working_dir; otherwise use a fixed
	// /work path (the tests that don't touch workspace-inline don't care
	// about a real dir since the RC lookup returns nil).
	workingDir := "/work"
	if mittorcContent != "" {
		workingDir = t.TempDir()
		if err := os.WriteFile(filepath.Join(workingDir, ".mittorc"), []byte(mittorcContent), 0644); err != nil {
			t.Fatalf("write .mittorc: %v", err)
		}
	}

	sm := conversation.NewSessionManager("", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-a", WorkingDir: workingDir, ACPServer: "test-server", IsDefault: true},
	})

	mittoConfig := &config.Config{}
	if settingsPrompts != nil {
		mittoConfig.Prompts = settingsPrompts
	}

	s := &Server{
		config: Config{
			PromptsCache: prompts.NewPromptsCache(),
			MittoConfig:  mittoConfig,
		},
		sessionManager: sm,
	}
	return s
}
