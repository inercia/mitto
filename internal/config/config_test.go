package config

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	defaultConfig "github.com/inercia/mitto/config"
)

func TestParse_ValidConfig(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
  - claude:
      command: "claude-code --acp"
prompts:
  - name: "Review"
    prompt: "Review this code"
web:
  host: "0.0.0.0"
  port: 9000
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 2 {
		t.Errorf("ACPServers count = %d, want 2", len(cfg.ACPServers))
	}

	if cfg.ACPServers[0].Name != "auggie" {
		t.Errorf("first server name = %q, want %q", cfg.ACPServers[0].Name, "auggie")
	}

	if cfg.ACPServers[0].Command != "auggie --acp" {
		t.Errorf("first server command = %q, want %q", cfg.ACPServers[0].Command, "auggie --acp")
	}

	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Web.Host = %q, want %q", cfg.Web.Host, "0.0.0.0")
	}

	if cfg.Web.Port != 9000 {
		t.Errorf("Web.Port = %d, want %d", cfg.Web.Port, 9000)
	}

	if len(cfg.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
}

func TestParse_TrustedProxyHeaders(t *testing.T) {
	yaml := `
web:
  security:
    trusted_proxies: [127.0.0.1]
    trusted_proxy_headers: [x-forwarded-for, cf-connecting-ip]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Web.Security == nil {
		t.Fatal("Web.Security is nil")
	}
	want := []string{"x-forwarded-for", "cf-connecting-ip"}
	if got := cfg.Web.Security.TrustedProxyHeaders; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("TrustedProxyHeaders = %v, want %v", got, want)
	}
}

func TestParse_ACPServerCwd(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      cwd: "/home/user/projects"
  - claude:
      command: "claude-code --acp"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 2 {
		t.Errorf("ACPServers count = %d, want 2", len(cfg.ACPServers))
	}

	// First server should have cwd set
	if cfg.ACPServers[0].Cwd != "/home/user/projects" {
		t.Errorf("first server cwd = %q, want %q", cfg.ACPServers[0].Cwd, "/home/user/projects")
	}

	// Second server should have empty cwd
	if cfg.ACPServers[1].Cwd != "" {
		t.Errorf("second server cwd = %q, want empty string", cfg.ACPServers[1].Cwd)
	}
}

func TestParse_ACPServerType(t *testing.T) {
	yaml := `
acp:
  - auggie-fast:
      command: "auggie --acp --model fast"
      type: auggie
  - auggie-smart:
      command: "auggie --acp --model smart"
      type: auggie
  - claude-code:
      command: "claude-code --acp"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 3 {
		t.Fatalf("ACPServers count = %d, want 3", len(cfg.ACPServers))
	}

	// First server should have type "auggie"
	if cfg.ACPServers[0].Type != "auggie" {
		t.Errorf("first server type = %q, want %q", cfg.ACPServers[0].Type, "auggie")
	}
	if cfg.ACPServers[0].GetType() != "auggie" {
		t.Errorf("first server GetType() = %q, want %q", cfg.ACPServers[0].GetType(), "auggie")
	}

	// Second server should also have type "auggie"
	if cfg.ACPServers[1].Type != "auggie" {
		t.Errorf("second server type = %q, want %q", cfg.ACPServers[1].Type, "auggie")
	}

	// Third server should have empty type (GetType falls back to name)
	if cfg.ACPServers[2].Type != "" {
		t.Errorf("third server type = %q, want empty string", cfg.ACPServers[2].Type)
	}
	if cfg.ACPServers[2].GetType() != "claude-code" {
		t.Errorf("third server GetType() = %q, want %q", cfg.ACPServers[2].GetType(), "claude-code")
	}

	// Test GetServerType on config
	if cfg.GetServerType("auggie-fast") != "auggie" {
		t.Errorf("GetServerType(auggie-fast) = %q, want %q", cfg.GetServerType("auggie-fast"), "auggie")
	}
	if cfg.GetServerType("claude-code") != "claude-code" {
		t.Errorf("GetServerType(claude-code) = %q, want %q", cfg.GetServerType("claude-code"), "claude-code")
	}
}

func TestParse_ACPServerTags(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      tags: [coding, fast]
  - claude:
      command: "claude-code --acp"
  - experimental:
      command: "exp --acp"
      tags: [testing, experimental, fast-model]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 3 {
		t.Fatalf("ACPServers count = %d, want 3", len(cfg.ACPServers))
	}

	// First server should have tags [coding, fast]
	if len(cfg.ACPServers[0].Tags) != 2 {
		t.Fatalf("first server tags count = %d, want 2", len(cfg.ACPServers[0].Tags))
	}
	if cfg.ACPServers[0].Tags[0] != "coding" {
		t.Errorf("first server tags[0] = %q, want %q", cfg.ACPServers[0].Tags[0], "coding")
	}
	if cfg.ACPServers[0].Tags[1] != "fast" {
		t.Errorf("first server tags[1] = %q, want %q", cfg.ACPServers[0].Tags[1], "fast")
	}

	// Second server should have no tags
	if len(cfg.ACPServers[1].Tags) != 0 {
		t.Errorf("second server tags count = %d, want 0", len(cfg.ACPServers[1].Tags))
	}

	// Third server should have tags [testing, experimental, fast-model]
	if len(cfg.ACPServers[2].Tags) != 3 {
		t.Fatalf("third server tags count = %d, want 3", len(cfg.ACPServers[2].Tags))
	}
	if cfg.ACPServers[2].Tags[0] != "testing" {
		t.Errorf("third server tags[0] = %q, want %q", cfg.ACPServers[2].Tags[0], "testing")
	}
	if cfg.ACPServers[2].Tags[2] != "fast-model" {
		t.Errorf("third server tags[2] = %q, want %q", cfg.ACPServers[2].Tags[2], "fast-model")
	}
}

func TestParse_EmptyACPServers(t *testing.T) {
	yaml := `
acp: []
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error for empty ACP servers: %v", err)
	}
	if len(cfg.ACPServers) != 0 {
		t.Errorf("expected 0 ACP servers, got %d", len(cfg.ACPServers))
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	yaml := `{{invalid yaml`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestParse_ExternalPort(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		expectedPort int
		description  string
	}{
		{
			name: "disabled",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: -1
`,
			expectedPort: -1,
			description:  "Port -1 means external listener is disabled",
		},
		{
			name: "random",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: 0
`,
			expectedPort: 0,
			description:  "Port 0 means OS chooses a random available port",
		},
		{
			name: "specific",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: 8443
`,
			expectedPort: 8443,
			description:  "Port > 0 means use that specific port",
		},
		{
			name: "not_specified",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  port: 8080
`,
			expectedPort: 0,
			description:  "When not specified, defaults to 0 (Go zero value)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			if cfg.Web.ExternalPort != tt.expectedPort {
				t.Errorf("ExternalPort = %d, want %d (%s)", cfg.Web.ExternalPort, tt.expectedPort, tt.description)
			}
		})
	}
}

func TestParse_WebHooks(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    up:
      command: "open http://localhost:${PORT}"
      name: "Open Browser"
    down:
      command: "echo Shutting down on port ${PORT}"
      name: "Cleanup"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Hooks.Up.Command != "open http://localhost:${PORT}" {
		t.Errorf("Up hook command = %q, want %q", cfg.Web.Hooks.Up.Command, "open http://localhost:${PORT}")
	}

	if cfg.Web.Hooks.Up.Name != "Open Browser" {
		t.Errorf("Up hook name = %q, want %q", cfg.Web.Hooks.Up.Name, "Open Browser")
	}

	if cfg.Web.Hooks.Down.Command != "echo Shutting down on port ${PORT}" {
		t.Errorf("Down hook command = %q, want %q", cfg.Web.Hooks.Down.Command, "echo Shutting down on port ${PORT}")
	}

	if cfg.Web.Hooks.Down.Name != "Cleanup" {
		t.Errorf("Down hook name = %q, want %q", cfg.Web.Hooks.Down.Name, "Cleanup")
	}
}

func TestParse_WebHooks_UpOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    up:
      command: "echo starting"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Hooks.Up.Command != "echo starting" {
		t.Errorf("Up hook command = %q, want %q", cfg.Web.Hooks.Up.Command, "echo starting")
	}

	// Down hook should be empty
	if cfg.Web.Hooks.Down.Command != "" {
		t.Errorf("Down hook command = %q, want empty", cfg.Web.Hooks.Down.Command)
	}
}

func TestParse_WebHooks_DownOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    down:
      command: "echo stopping"
      name: "Stop"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Up hook should be empty
	if cfg.Web.Hooks.Up.Command != "" {
		t.Errorf("Up hook command = %q, want empty", cfg.Web.Hooks.Up.Command)
	}

	if cfg.Web.Hooks.Down.Command != "echo stopping" {
		t.Errorf("Down hook command = %q, want %q", cfg.Web.Hooks.Down.Command, "echo stopping")
	}

	if cfg.Web.Hooks.Down.Name != "Stop" {
		t.Errorf("Down hook name = %q, want %q", cfg.Web.Hooks.Down.Name, "Stop")
	}
}

func TestParse_PerServerPrompts(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Improve Rules"
          prompt: "Please improve the rules"
        - name: "Run Tests"
          prompt: "Run all tests and fix failures"
  - claude:
      command: "claude-code --acp"
prompts:
  - name: "Continue"
    prompt: "Continue with the task"
web:
  host: "127.0.0.1"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check that auggie has 2 prompts
	if len(cfg.ACPServers) != 2 {
		t.Fatalf("ACPServers count = %d, want 2", len(cfg.ACPServers))
	}

	auggie := cfg.ACPServers[0]
	if auggie.Name != "auggie" {
		t.Errorf("first server name = %q, want %q", auggie.Name, "auggie")
	}
	if len(auggie.Prompts) != 2 {
		t.Fatalf("auggie prompts count = %d, want 2", len(auggie.Prompts))
	}
	if auggie.Prompts[0].Name != "Improve Rules" {
		t.Errorf("auggie first prompt name = %q, want %q", auggie.Prompts[0].Name, "Improve Rules")
	}
	if auggie.Prompts[1].Prompt != "Run all tests and fix failures" {
		t.Errorf("auggie second prompt text = %q, want %q", auggie.Prompts[1].Prompt, "Run all tests and fix failures")
	}

	// Check that claude has no prompts
	claude := cfg.ACPServers[1]
	if len(claude.Prompts) != 0 {
		t.Errorf("claude prompts count = %d, want 0", len(claude.Prompts))
	}

	// Check global prompts are still parsed
	if len(cfg.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
}

// TestParse_InlineMenus_WarnsOnUnknownTokenOnEveryPath pins mitto-rjg6 across
// all THREE inline-prompt sources Parse handles: the top-level prompts: block
// and the per-ACP-server prompts: block (the latter was initially missed —
// only the top-level and .mittorc paths were wired). Each must emit a WARN
// naming the prompt and the offending token while still loading the prompt.
func TestParse_InlineMenus_WarnsOnUnknownTokenOnEveryPath(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Per Server Typo"
          prompt: "body"
          menus: "prompts, conversations"
        - name: "Per Server Valid"
          prompt: "body"
          menus: "internal"
prompts:
  - name: "Top Level Typo"
    prompt: "body"
    menus: "prompts, beadsIssue"
  - name: "Top Level Valid"
    prompt: "body"
    menus: "prompts, !promptsLoop"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Non-fatal: every prompt still loads, typo or not.
	if got := len(cfg.ACPServers[0].Prompts); got != 2 {
		t.Errorf("per-server prompts count = %d, want 2 (warning must not drop prompts)", got)
	}
	if got := len(cfg.Prompts); got != 2 {
		t.Errorf("top-level prompts count = %d, want 2 (warning must not drop prompts)", got)
	}

	out := buf.String()
	for _, want := range []string{"Per Server Typo", "conversations", "Top Level Typo", "beadsIssue"} {
		if !strings.Contains(out, want) {
			t.Errorf("Parse did not warn as expected; missing %q in log: %s", want, out)
		}
	}
	for _, unwanted := range []string{"Per Server Valid", "Top Level Valid"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("Parse warned for a valid menus value (%s); log: %s", unwanted, out)
		}
	}
}

// TestParse_InlineLoop_FlatSchemaMigratedInMemory pins mitto-opoh: prior to
// the fix, a global or per-server inline prompt's pre-r6j flat loop: block
// decoded straight into *PromptLoop and hit PromptLoop.UnmarshalYAML's
// strict rejection, hard-failing the ENTIRE settings.yaml Parse call. It
// must now migrate in memory (via DecodeInlineLoop) instead.
func TestParse_InlineLoop_FlatSchemaMigratedInMemory(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Server Loop"
          prompt: "do something"
          loop:
            trigger: onCompletion
            delay: 20
prompts:
  - name: "Global Loop"
    prompt: "do something else"
    loop:
      trigger: onCompletion
      delay: 30
      maxIterations: 10
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
	loop := cfg.Prompts[0].Loop
	if loop == nil {
		t.Fatal("global prompt Loop = nil, want migrated PromptLoop")
	}
	if len(loop.Trigger) != 1 || loop.Trigger[0] != "onCompletion" {
		t.Errorf("global Trigger = %v, want [onCompletion]", loop.Trigger)
	}
	if loop.OnCompletion == nil || loop.OnCompletion.Delay != 30 {
		t.Errorf("global OnCompletion = %+v, want Delay=30", loop.OnCompletion)
	}
	if loop.MaxIterations != 10 {
		t.Errorf("global MaxIterations = %d, want 10", loop.MaxIterations)
	}

	if len(cfg.ACPServers) != 1 || len(cfg.ACPServers[0].Prompts) != 1 {
		t.Fatalf("ACPServers = %+v, want 1 server with 1 prompt", cfg.ACPServers)
	}
	serverLoop := cfg.ACPServers[0].Prompts[0].Loop
	if serverLoop == nil {
		t.Fatal("server prompt Loop = nil, want migrated PromptLoop")
	}
	if serverLoop.OnCompletion == nil || serverLoop.OnCompletion.Delay != 20 {
		t.Errorf("server OnCompletion = %+v, want Delay=20", serverLoop.OnCompletion)
	}
}

// TestParse_InlineLoop_InvalidDropsLoopKeepsPrompt pins mitto-opoh's other
// settings.yaml/ACP-server graceful-degradation guarantee: an inline loop:
// block that still fails validation after migration (e.g. an unknown mode)
// must only drop that prompt's loop config, not the prompt itself, and must
// not fail the whole Parse call — for both the global prompts: list and a
// per-ACP-server prompts: list.
func TestParse_InlineLoop_InvalidDropsLoopKeepsPrompt(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Bad Server Loop"
          prompt: "do something"
          loop:
            mode: bogus
prompts:
  - name: "Bad Global Loop"
    prompt: "do something else"
    loop:
      mode: bogus
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1 (prompt kept despite bad loop)", len(cfg.Prompts))
	}
	if cfg.Prompts[0].Loop != nil {
		t.Errorf("global Loop = %+v, want nil for invalid mode", cfg.Prompts[0].Loop)
	}

	if len(cfg.ACPServers) != 1 || len(cfg.ACPServers[0].Prompts) != 1 {
		t.Fatalf("ACPServers = %+v, want 1 server with 1 prompt kept despite bad loop", cfg.ACPServers)
	}
	if cfg.ACPServers[0].Prompts[0].Loop != nil {
		t.Errorf("server Loop = %+v, want nil for invalid mode", cfg.ACPServers[0].Prompts[0].Loop)
	}
}

func TestParse_PromptBackgroundColor(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Server Prompt"
          prompt: "Server prompt text"
          backgroundColor: "#FF5733"
prompts:
  - name: "Global Prompt"
    prompt: "Global prompt text"
    backgroundColor: "#E8F5E9"
  - name: "No Color"
    prompt: "Prompt without color"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check server prompt has backgroundColor
	if len(cfg.ACPServers[0].Prompts) != 1 {
		t.Fatalf("server prompts count = %d, want 1", len(cfg.ACPServers[0].Prompts))
	}
	if cfg.ACPServers[0].Prompts[0].BackgroundColor != "#FF5733" {
		t.Errorf("server prompt backgroundColor = %q, want %q", cfg.ACPServers[0].Prompts[0].BackgroundColor, "#FF5733")
	}

	// Check global prompts
	if len(cfg.Prompts) != 2 {
		t.Fatalf("global prompts count = %d, want 2", len(cfg.Prompts))
	}
	if cfg.Prompts[0].BackgroundColor != "#E8F5E9" {
		t.Errorf("first global prompt backgroundColor = %q, want %q", cfg.Prompts[0].BackgroundColor, "#E8F5E9")
	}
	if cfg.Prompts[1].BackgroundColor != "" {
		t.Errorf("second global prompt backgroundColor = %q, want empty", cfg.Prompts[1].BackgroundColor)
	}
}

// TestParse_PromptSingletonAndTags verifies that top-level (global) inline
// `prompts:` entries in config.yaml carry `singleton` and `tags` through
// Parse() into the resulting WebPrompt (mitto-4mb.7). A prompt that omits
// both keys must default to Singleton=false and empty Tags, guarding
// against accidental coupling between sibling prompt entries.
func TestParse_PromptSingletonAndTags(t *testing.T) {
	yaml := `
prompts:
  - name: "Singleton Prompt"
    prompt: "Singleton prompt text"
    singleton: true
    tags: ["foo", "bar"]
  - name: "Plain Prompt"
    prompt: "Plain prompt text"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Prompts) != 2 {
		t.Fatalf("global prompts count = %d, want 2", len(cfg.Prompts))
	}

	singleton := cfg.Prompts[0]
	if !singleton.Singleton {
		t.Errorf("singleton prompt Singleton = %v, want true", singleton.Singleton)
	}
	if len(singleton.Tags) != 2 || singleton.Tags[0] != "foo" || singleton.Tags[1] != "bar" {
		t.Errorf("singleton prompt Tags = %v, want [foo bar]", singleton.Tags)
	}

	plain := cfg.Prompts[1]
	if plain.Singleton {
		t.Errorf("plain prompt Singleton = %v, want false", plain.Singleton)
	}
	if len(plain.Tags) != 0 {
		t.Errorf("plain prompt Tags = %v, want empty", plain.Tags)
	}
}

// TestParse_PerServerPromptSingletonAndTags is the per-ACP-server counterpart
// of TestParse_PromptSingletonAndTags: it verifies the same round-trip for
// prompts nested under `acp[].prompts:` (mitto-4mb.7).
func TestParse_PerServerPromptSingletonAndTags(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Singleton Server Prompt"
          prompt: "Singleton server prompt text"
          singleton: true
          tags: ["foo", "bar"]
        - name: "Plain Server Prompt"
          prompt: "Plain server prompt text"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Fatalf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
	auggie := cfg.ACPServers[0]
	if len(auggie.Prompts) != 2 {
		t.Fatalf("auggie prompts count = %d, want 2", len(auggie.Prompts))
	}

	singleton := auggie.Prompts[0]
	if !singleton.Singleton {
		t.Errorf("singleton server prompt Singleton = %v, want true", singleton.Singleton)
	}
	if len(singleton.Tags) != 2 || singleton.Tags[0] != "foo" || singleton.Tags[1] != "bar" {
		t.Errorf("singleton server prompt Tags = %v, want [foo bar]", singleton.Tags)
	}

	plain := auggie.Prompts[1]
	if plain.Singleton {
		t.Errorf("plain server prompt Singleton = %v, want false", plain.Singleton)
	}
	if len(plain.Tags) != 0 {
		t.Errorf("plain server prompt Tags = %v, want empty", plain.Tags)
	}
}

func TestParse_PromptsDirs(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
prompts_dirs:
  - "/custom/prompts"
  - "/shared/team/prompts"
  - "relative/prompts"
prompts:
  - name: "Inline"
    prompt: "Inline prompt"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.PromptsDirs) != 3 {
		t.Fatalf("PromptsDirs count = %d, want 3", len(cfg.PromptsDirs))
	}

	if cfg.PromptsDirs[0] != "/custom/prompts" {
		t.Errorf("PromptsDirs[0] = %q, want %q", cfg.PromptsDirs[0], "/custom/prompts")
	}

	if cfg.PromptsDirs[1] != "/shared/team/prompts" {
		t.Errorf("PromptsDirs[1] = %q, want %q", cfg.PromptsDirs[1], "/shared/team/prompts")
	}

	if cfg.PromptsDirs[2] != "relative/prompts" {
		t.Errorf("PromptsDirs[2] = %q, want %q", cfg.PromptsDirs[2], "relative/prompts")
	}

	// Should also have the inline prompt
	if len(cfg.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
}

func TestParse_PromptsDirsEmpty(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
prompts_dirs: []
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.PromptsDirs) != 0 {
		t.Errorf("PromptsDirs count = %d, want 0", len(cfg.PromptsDirs))
	}
}

func TestParse_NoPromptsDirs(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.PromptsDirs) != 0 {
		t.Errorf("PromptsDirs = %v, want empty", cfg.PromptsDirs)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".mittorc")

	yaml := `
acp:
  - test:
      command: "test-cmd"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/.mittorc")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestLoad_JSONFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	jsonConfig := `{
		"acp_servers": [
			{"name": "json-server", "command": "json-cmd --acp"}
		],
		"web": {
			"host": "0.0.0.0",
			"port": 9000
		}
	}`
	if err := os.WriteFile(path, []byte(jsonConfig), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].Name != "json-server" {
		t.Errorf("server name = %q, want %q", cfg.ACPServers[0].Name, "json-server")
	}
	if cfg.ACPServers[0].Command != "json-cmd --acp" {
		t.Errorf("server command = %q, want %q", cfg.ACPServers[0].Command, "json-cmd --acp")
	}
	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Web.Host = %q, want %q", cfg.Web.Host, "0.0.0.0")
	}
	if cfg.Web.Port != 9000 {
		t.Errorf("Web.Port = %d, want %d", cfg.Web.Port, 9000)
	}
}

func TestLoad_YAMLFileWithYmlExtension(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yml")

	yamlConfig := `
acp:
  - yml-server:
      command: "yml-cmd --acp"
`
	if err := os.WriteFile(path, []byte(yamlConfig), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].Name != "yml-server" {
		t.Errorf("server name = %q, want %q", cfg.ACPServers[0].Name, "yml-server")
	}
}

func TestParseJSON_EmptyACPServers(t *testing.T) {
	jsonConfig := `{"acp_servers": []}`
	cfg, err := ParseJSON([]byte(jsonConfig))
	if err != nil {
		t.Fatalf("unexpected error for empty ACP servers: %v", err)
	}
	if len(cfg.ACPServers) != 0 {
		t.Errorf("expected 0 ACP servers, got %d", len(cfg.ACPServers))
	}
}

func TestDefaultServer(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "first", Command: "cmd1"},
			{Name: "second", Command: "cmd2"},
		},
	}

	srv := cfg.DefaultServer()
	if srv == nil {
		t.Fatal("DefaultServer returned nil")
	}

	if srv.Name != "first" {
		t.Errorf("DefaultServer name = %q, want %q", srv.Name, "first")
	}
}

func TestDefaultServer_Empty(t *testing.T) {
	cfg := &Config{ACPServers: []ACPServer{}}
	srv := cfg.DefaultServer()
	if srv != nil {
		t.Errorf("DefaultServer = %v, want nil for empty config", srv)
	}
}

func TestGetServer(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "auggie", Command: "auggie --acp"},
			{Name: "claude", Command: "claude-code --acp"},
		},
	}

	tests := []struct {
		name       string
		serverName string
		wantErr    bool
		wantCmd    string
	}{
		{"existing server", "auggie", false, "auggie --acp"},
		{"second server", "claude", false, "claude-code --acp"},
		{"non-existent server", "unknown", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := cfg.GetServer(tt.serverName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if srv.Command != tt.wantCmd {
				t.Errorf("Command = %q, want %q", srv.Command, tt.wantCmd)
			}
		})
	}
}

func TestServerNames(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "auggie", Command: "cmd1"},
			{Name: "claude", Command: "cmd2"},
			{Name: "gemini", Command: "cmd3"},
		},
	}

	names := cfg.ServerNames()

	if len(names) != 3 {
		t.Fatalf("ServerNames count = %d, want 3", len(names))
	}

	expected := []string{"auggie", "claude", "gemini"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestServerNames_Empty(t *testing.T) {
	cfg := &Config{ACPServers: []ACPServer{}}
	names := cfg.ServerNames()

	if len(names) != 0 {
		t.Errorf("ServerNames = %v, want empty slice", names)
	}
}

func TestACPServer_GetType(t *testing.T) {
	tests := []struct {
		name     string
		server   ACPServer
		wantType string
	}{
		{
			name:     "type set explicitly",
			server:   ACPServer{Name: "auggie-fast", Type: "auggie"},
			wantType: "auggie",
		},
		{
			name:     "type not set - falls back to name",
			server:   ACPServer{Name: "claude-code"},
			wantType: "claude-code",
		},
		{
			name:     "empty type - falls back to name",
			server:   ACPServer{Name: "my-server", Type: ""},
			wantType: "my-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.server.GetType()
			if got != tt.wantType {
				t.Errorf("GetType() = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestConfig_GetServerType(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "auggie-fast", Type: "auggie"},
			{Name: "auggie-smart", Type: "auggie"},
			{Name: "claude-code"}, // No type - should return name
		},
	}

	tests := []struct {
		name       string
		serverName string
		wantType   string
	}{
		{
			name:       "server with explicit type",
			serverName: "auggie-fast",
			wantType:   "auggie",
		},
		{
			name:       "another server with same type",
			serverName: "auggie-smart",
			wantType:   "auggie",
		},
		{
			name:       "server without type - falls back to name",
			serverName: "claude-code",
			wantType:   "claude-code",
		},
		{
			name:       "non-existent server - returns empty",
			serverName: "unknown",
			wantType:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.GetServerType(tt.serverName)
			if got != tt.wantType {
				t.Errorf("GetServerType(%q) = %q, want %q", tt.serverName, got, tt.wantType)
			}
		})
	}
}

func TestParse_StaticDir(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  static_dir: "/path/to/static"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.StaticDir != "/path/to/static" {
		t.Errorf("Web.StaticDir = %q, want %q", cfg.Web.StaticDir, "/path/to/static")
	}
}

func TestParse_AuthSimple(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
      username: "admin"
      password: "secret123"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil, expected auth config")
	}

	if cfg.Web.Auth.Simple == nil {
		t.Fatal("Web.Auth.Simple is nil, expected simple auth config")
	}

	if cfg.Web.Auth.Simple.Username != "admin" {
		t.Errorf("Web.Auth.Simple.Username = %q, want %q", cfg.Web.Auth.Simple.Username, "admin")
	}

	if cfg.Web.Auth.Simple.Password != "secret123" {
		t.Errorf("Web.Auth.Simple.Password = %q, want %q", cfg.Web.Auth.Simple.Password, "secret123")
	}
}

func TestParse_NoAuth(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  port: 8080
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth != nil {
		t.Errorf("Web.Auth = %v, want nil when auth is not configured", cfg.Web.Auth)
	}
}

func TestParse_AuthEmptySimple(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// When auth section is present but simple is empty/nil in YAML,
	// WebAuth is created but Simple is nil
	// This allows for auth config with only allow list
	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil when auth section is present")
	}

	if cfg.Web.Auth.Simple != nil {
		t.Errorf("Web.Auth.Simple = %v, want nil when simple is empty", cfg.Web.Auth.Simple)
	}
}

func TestParse_AuthAllow(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
      username: "admin"
      password: "secret"
    allow:
      ips:
        - "127.0.0.1"
        - "::1"
        - "192.168.0.0/24"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil")
	}

	if cfg.Web.Auth.Allow == nil {
		t.Fatal("Web.Auth.Allow is nil")
	}

	if len(cfg.Web.Auth.Allow.IPs) != 3 {
		t.Fatalf("Web.Auth.Allow.IPs length = %d, want 3", len(cfg.Web.Auth.Allow.IPs))
	}

	expected := []string{"127.0.0.1", "::1", "192.168.0.0/24"}
	for i, want := range expected {
		if cfg.Web.Auth.Allow.IPs[i] != want {
			t.Errorf("Web.Auth.Allow.IPs[%d] = %q, want %q", i, cfg.Web.Auth.Allow.IPs[i], want)
		}
	}
}

func TestParse_AuthAllowOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    allow:
      ips:
        - "127.0.0.1"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil when allow is configured")
	}

	if cfg.Web.Auth.Simple != nil {
		t.Error("Web.Auth.Simple should be nil when only allow is configured")
	}

	if cfg.Web.Auth.Allow == nil {
		t.Fatal("Web.Auth.Allow is nil")
	}

	if len(cfg.Web.Auth.Allow.IPs) != 1 {
		t.Fatalf("Web.Auth.Allow.IPs length = %d, want 1", len(cfg.Web.Auth.Allow.IPs))
	}

	if cfg.Web.Auth.Allow.IPs[0] != "127.0.0.1" {
		t.Errorf("Web.Auth.Allow.IPs[0] = %q, want %q", cfg.Web.Auth.Allow.IPs[0], "127.0.0.1")
	}
}

func TestParse_UIHotkeys(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        key: "ctrl+alt+m"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Hotkeys == nil {
		t.Fatal("UI.Mac.Hotkeys is nil")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI.Mac.Hotkeys.ShowHide is nil")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide.Key != "ctrl+alt+m" {
		t.Errorf("ShowHide.Key = %q, want %q", cfg.UI.Mac.Hotkeys.ShowHide.Key, "ctrl+alt+m")
	}

	// Test GetShowHideHotkey helper
	key, enabled := cfg.GetShowHideHotkey()
	if !enabled {
		t.Error("GetShowHideHotkey returned enabled=false, want true")
	}
	if key != "ctrl+alt+m" {
		t.Errorf("GetShowHideHotkey key = %q, want %q", key, "ctrl+alt+m")
	}
}

func TestParse_UIHotkeysDisabled(t *testing.T) {
	disabled := false
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil || cfg.UI.Mac.Hotkeys == nil || cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI config not properly parsed")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide.Enabled == nil {
		t.Fatal("ShowHide.Enabled is nil")
	}

	if *cfg.UI.Mac.Hotkeys.ShowHide.Enabled != disabled {
		t.Errorf("ShowHide.Enabled = %v, want %v", *cfg.UI.Mac.Hotkeys.ShowHide.Enabled, disabled)
	}

	// Test GetShowHideHotkey helper
	_, enabled := cfg.GetShowHideHotkey()
	if enabled {
		t.Error("GetShowHideHotkey returned enabled=true, want false")
	}
}

func TestGetShowHideHotkey_Default(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	key, enabled := cfg.GetShowHideHotkey()
	if !enabled {
		t.Error("GetShowHideHotkey returned enabled=false, want true")
	}
	if key != DefaultShowHideHotkey {
		t.Errorf("GetShowHideHotkey key = %q, want %q", key, DefaultShowHideHotkey)
	}
}

func TestParse_UINotificationsSounds(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Notifications == nil {
		t.Fatal("UI.Mac.Notifications is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications.Sounds is nil")
	}

	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UINotificationsSoundsDisabled(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    notifications:
      sounds:
        agent_completed: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Notifications == nil {
		t.Fatal("UI.Mac.Notifications is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications.Sounds is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = true, want false")
	}
}

func TestParse_UIBothHotkeysAndNotifications(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        key: "cmd+alt+m"
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check hotkeys
	if cfg.UI.Mac == nil || cfg.UI.Mac.Hotkeys == nil || cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI.Mac.Hotkeys not properly parsed")
	}
	if cfg.UI.Mac.Hotkeys.ShowHide.Key != "cmd+alt+m" {
		t.Errorf("ShowHide.Key = %q, want %q", cfg.UI.Mac.Hotkeys.ShowHide.Key, "cmd+alt+m")
	}

	// Check notifications
	if cfg.UI.Mac.Notifications == nil || cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications not properly parsed")
	}
	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UIConfirmations(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_conversation: never
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.DeleteConversation != DeleteConversationNever {
		t.Errorf("Confirmations.DeleteConversation = %q, want %q", cfg.UI.Confirmations.DeleteConversation, DeleteConversationNever)
	}
}

func TestParse_UIConfirmationsAlways(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_conversation: always
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.DeleteConversation != DeleteConversationAlways {
		t.Errorf("Confirmations.DeleteConversation = %q, want %q", cfg.UI.Confirmations.DeleteConversation, DeleteConversationAlways)
	}
}

func TestParse_UIConfirmationsWithMac(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_conversation: never
  mac:
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check confirmations
	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations not properly parsed")
	}
	if cfg.UI.Confirmations.DeleteConversation != DeleteConversationNever {
		t.Errorf("Confirmations.DeleteConversation = %q, want %q", cfg.UI.Confirmations.DeleteConversation, DeleteConversationNever)
	}

	// Check Mac notifications
	if cfg.UI.Mac == nil || cfg.UI.Mac.Notifications == nil || cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications not properly parsed")
	}
	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UIConfirmationsDeleteConversationResponding(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_conversation: responding
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.DeleteConversation != DeleteConversationResponding {
		t.Errorf("Confirmations.DeleteConversation = %q, want %q", cfg.UI.Confirmations.DeleteConversation, DeleteConversationResponding)
	}

	// "responding" still confirms before quitting with a responding agent.
	if cfg.ShouldConfirmDeleteRespondingSession() != true {
		t.Error("ShouldConfirmDeleteRespondingSession() = false, want true")
	}

	if cfg.DeleteConversationMode() != DeleteConversationResponding {
		t.Errorf("DeleteConversationMode() = %q, want %q", cfg.DeleteConversationMode(), DeleteConversationResponding)
	}
}

func TestParse_UIStartAtLogin(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    start_at_login: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if !cfg.UI.Mac.StartAtLogin {
		t.Error("UI.Mac.StartAtLogin = false, want true")
	}
}

func TestParse_UIStartAtLoginFalse(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    start_at_login: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.StartAtLogin {
		t.Error("UI.Mac.StartAtLogin = true, want false")
	}
}

func TestParse_UIOpenIn(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    open_in:
      targets:
        - id: finder
          enabled: true
          command: "code ${MITTO_WORKING_DIR}"
        - id: my-editor
          label: "My Editor"
          command: "my-editor ${MITTO_WORKING_DIR}"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}
	if cfg.UI.Mac.OpenIn == nil {
		t.Fatal("UI.Mac.OpenIn is nil")
	}
	if got := len(cfg.UI.Mac.OpenIn.Targets); got != 2 {
		t.Fatalf("UI.Mac.OpenIn.Targets length = %d, want 2", got)
	}

	first := cfg.UI.Mac.OpenIn.Targets[0]
	if first.ID != "finder" {
		t.Errorf("Targets[0].ID = %q, want %q", first.ID, "finder")
	}
	if first.Enabled == nil || !*first.Enabled {
		t.Errorf("Targets[0].Enabled = %v, want *true", first.Enabled)
	}
	if first.Command != "code ${MITTO_WORKING_DIR}" {
		t.Errorf("Targets[0].Command = %q", first.Command)
	}

	second := cfg.UI.Mac.OpenIn.Targets[1]
	if second.ID != "my-editor" {
		t.Errorf("Targets[1].ID = %q, want %q", second.ID, "my-editor")
	}
	if second.Label != "My Editor" {
		t.Errorf("Targets[1].Label = %q", second.Label)
	}
	if second.Command != "my-editor ${MITTO_WORKING_DIR}" {
		t.Errorf("Targets[1].Command = %q", second.Command)
	}
}

func TestOpenTarget_GetEnabled_Defaults(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	builtinUnset := &OpenTarget{ID: "finder", Builtin: true}
	if !builtinUnset.GetEnabled() {
		t.Error("builtin target with nil Enabled should default to true")
	}

	userUnset := &OpenTarget{ID: "custom", Builtin: false}
	if userUnset.GetEnabled() {
		t.Error("user-defined target with nil Enabled should default to false")
	}

	explicitFalse := &OpenTarget{ID: "finder", Builtin: true, Enabled: boolPtr(false)}
	if explicitFalse.GetEnabled() {
		t.Error("explicit Enabled=false should return false even for builtin")
	}

	explicitTrue := &OpenTarget{ID: "custom", Builtin: false, Enabled: boolPtr(true)}
	if !explicitTrue.GetEnabled() {
		t.Error("explicit Enabled=true should return true even for user target")
	}
}

func TestDefaultOpenTargets_MacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("skipping darwin-specific defaults on %s", runtime.GOOS)
	}
	targets := DefaultOpenTargets()
	if len(targets) != 7 {
		t.Fatalf("DefaultOpenTargets length = %d, want 7", len(targets))
	}
	if targets[0].ID != "finder" {
		t.Errorf("targets[0].ID = %q, want %q", targets[0].ID, "finder")
	}
	if targets[1].ID != "terminal" {
		t.Errorf("targets[1].ID = %q, want %q", targets[1].ID, "terminal")
	}
	enabledByDefault := map[string]bool{
		"finder":   true,
		"terminal": true,
		"iterm":    false,
		"vscode":   false,
		"cursor":   false,
		"xcode":    false,
		"goland":   false,
	}
	for _, tgt := range targets {
		if !tgt.Builtin {
			t.Errorf("target %q should have Builtin=true", tgt.ID)
		}
		want, ok := enabledByDefault[tgt.ID]
		if !ok {
			t.Errorf("unexpected default target id %q", tgt.ID)
			continue
		}
		if got := tgt.GetEnabled(); got != want {
			t.Errorf("target %q GetEnabled() = %v, want %v", tgt.ID, got, want)
		}
	}
}

func TestEffectiveOpenTargets_EmptyReturnsDefaults(t *testing.T) {
	// With OpenIn nil or empty, EffectiveOpenTargets must return the platform
	// defaults verbatim (no legacy synthesis).
	want := DefaultOpenTargets()

	c := &MacUIConfig{}
	got := c.EffectiveOpenTargets()
	if len(got) != len(want) {
		t.Fatalf("nil OpenIn: length = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].ID != want[i].ID {
			t.Errorf("nil OpenIn: got[%d].ID = %q, want %q", i, got[i].ID, want[i].ID)
		}
	}

	c2 := &MacUIConfig{OpenIn: &OpenInConfig{Targets: nil}}
	got2 := c2.EffectiveOpenTargets()
	if len(got2) != len(want) {
		t.Fatalf("empty Targets: length = %d, want %d", len(got2), len(want))
	}
}

func TestEffectiveOpenTargets_UserOverridesMergeById(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("skipping darwin-specific merge test on %s", runtime.GOOS)
	}
	boolPtr := func(b bool) *bool { return &b }
	c := &MacUIConfig{
		OpenIn: &OpenInConfig{
			Targets: []OpenTarget{
				{ID: "vscode", Enabled: boolPtr(true)},
				{ID: "my-editor", Label: "My Editor", Command: "my-editor ${MITTO_WORKING_DIR}"},
			},
		},
	}
	got := c.EffectiveOpenTargets()
	defaults := DefaultOpenTargets()
	if len(got) != len(defaults)+1 {
		t.Fatalf("EffectiveOpenTargets length = %d, want %d", len(got), len(defaults)+1)
	}
	// Builtin order preserved.
	for i, d := range defaults {
		if got[i].ID != d.ID {
			t.Errorf("got[%d].ID = %q, want %q", i, got[i].ID, d.ID)
		}
	}
	// vscode override applied.
	var vscode *OpenTarget
	for i := range got {
		if got[i].ID == "vscode" {
			vscode = &got[i]
			break
		}
	}
	if vscode == nil {
		t.Fatal("vscode entry missing from merged result")
	}
	if !vscode.GetEnabled() {
		t.Error("vscode should be enabled after user override")
	}
	if !vscode.Builtin {
		t.Error("vscode should still be Builtin after override")
	}
	// my-editor appended last, Builtin=false.
	last := got[len(got)-1]
	if last.ID != "my-editor" {
		t.Errorf("last.ID = %q, want %q", last.ID, "my-editor")
	}
	if last.Builtin {
		t.Error("user-defined target must not be Builtin")
	}
	// No duplicates.
	seen := make(map[string]int)
	for _, tgt := range got {
		seen[tgt.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("duplicate id %q (count=%d)", id, n)
		}
	}
}

func TestEffectiveOpenTargets_NilReceiver(t *testing.T) {
	var c *MacUIConfig
	got := c.EffectiveOpenTargets()
	want := DefaultOpenTargets()
	if len(got) != len(want) {
		t.Fatalf("nil receiver length = %d, want %d", len(got), len(want))
	}
	if len(want) > 0 && got[0].ID != want[0].ID {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, want[0].ID)
	}
}

func TestShouldConfirmDeleteRespondingSession(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name:     "nil confirmations returns true",
			config:   &Config{},
			expected: true,
		},
		{
			name: "empty mode returns true",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{},
				},
			},
			expected: true,
		},
		{
			name: "always returns true",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{
						DeleteConversation: DeleteConversationAlways,
					},
				},
			},
			expected: true,
		},
		{
			name: "responding returns true",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{
						DeleteConversation: DeleteConversationResponding,
					},
				},
			},
			expected: true,
		},
		{
			name: "never returns false",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{
						DeleteConversation: DeleteConversationNever,
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.ShouldConfirmDeleteRespondingSession()
			if got != tt.expected {
				t.Errorf("ShouldConfirmDeleteRespondingSession() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDeleteConversationMode(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected string
	}{
		{
			name:     "nil confirmations defaults to always",
			config:   &Config{},
			expected: DeleteConversationAlways,
		},
		{
			name: "empty value defaults to always",
			config: &Config{
				UI: UIConfig{Confirmations: &ConfirmationsConfig{}},
			},
			expected: DeleteConversationAlways,
		},
		{
			name: "invalid value defaults to always",
			config: &Config{
				UI: UIConfig{Confirmations: &ConfirmationsConfig{DeleteConversation: "bogus"}},
			},
			expected: DeleteConversationAlways,
		},
		{
			name: "responding is preserved",
			config: &Config{
				UI: UIConfig{Confirmations: &ConfirmationsConfig{DeleteConversation: DeleteConversationResponding}},
			},
			expected: DeleteConversationResponding,
		},
		{
			name: "never is preserved",
			config: &Config{
				UI: UIConfig{Confirmations: &ConfirmationsConfig{DeleteConversation: DeleteConversationNever}},
			},
			expected: DeleteConversationNever,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.DeleteConversationMode()
			if got != tt.expected {
				t.Errorf("DeleteConversationMode() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// Tests for MessageProcessor
// Note: ShouldApply, Apply, and ApplyProcessors have been removed from the config package.
// Message processing is now done via the unified processor pipeline in internal/processors.
// See MergeProcessors and Manager.AddTextProcessors for the new API.

func TestMessageProcessor_Fields(t *testing.T) {
	// Verify the struct fields are correct (used for config parsing).
	p := &MessageProcessor{
		When:   ProcessorWhenBlock{On: ProcessorPhaseUserPrompt, Match: ProcessorMatchFirst},
		Mutate: ProcessorMutatePrepend,
		Text:   "hello",
	}
	if p.When.On != ProcessorPhaseUserPrompt {
		t.Errorf("When.On = %q, want %q", p.When.On, ProcessorPhaseUserPrompt)
	}
	if p.When.Match != ProcessorMatchFirst {
		t.Errorf("When.Match = %q, want %q", p.When.Match, ProcessorMatchFirst)
	}
	if p.Mutate != ProcessorMutatePrepend {
		t.Errorf("Mutate = %q, want %q", p.Mutate, ProcessorMutatePrepend)
	}
	if p.Text != "hello" {
		t.Errorf("Text = %q, want %q", p.Text, "hello")
	}
}

func TestMergeProcessors(t *testing.T) {
	globalProcessors := []MessageProcessor{
		{When: ProcessorWhenBlock{On: ProcessorPhaseUserPrompt, Match: ProcessorMatchAll}, Mutate: ProcessorMutateAppend, Text: ":GLOBAL"},
	}
	workspaceProcessors := []MessageProcessor{
		{When: ProcessorWhenBlock{On: ProcessorPhaseUserPrompt, Match: ProcessorMatchFirst}, Mutate: ProcessorMutatePrepend, Text: "WORKSPACE:"},
	}

	global := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: globalProcessors},
	}
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: workspaceProcessors},
	}

	// Test merge (global first, then workspace)
	merged := MergeProcessors(global, workspace)
	if len(merged) != 2 {
		t.Fatalf("MergeProcessors returned %d processors, want 2", len(merged))
	}
	if merged[0].Text != ":GLOBAL" {
		t.Errorf("First processor text = %q, want %q", merged[0].Text, ":GLOBAL")
	}
	if merged[1].Text != "WORKSPACE:" {
		t.Errorf("Second processor text = %q, want %q", merged[1].Text, "WORKSPACE:")
	}
}

func TestMergeProcessors_Override(t *testing.T) {
	globalProcessors := []MessageProcessor{
		{When: ProcessorWhenBlock{On: ProcessorPhaseUserPrompt, Match: ProcessorMatchAll}, Mutate: ProcessorMutateAppend, Text: ":GLOBAL"},
	}
	workspaceProcessors := []MessageProcessor{
		{When: ProcessorWhenBlock{On: ProcessorPhaseUserPrompt, Match: ProcessorMatchFirst}, Mutate: ProcessorMutatePrepend, Text: "WORKSPACE:"},
	}

	global := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: globalProcessors},
	}
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Override:   true,
			Processors: workspaceProcessors,
		},
	}

	// Test override (only workspace processors)
	merged := MergeProcessors(global, workspace)
	if len(merged) != 1 {
		t.Fatalf("MergeProcessors with override returned %d processors, want 1", len(merged))
	}
	if merged[0].Text != "WORKSPACE:" {
		t.Errorf("Processor text = %q, want %q", merged[0].Text, "WORKSPACE:")
	}
}

func TestMergeProcessors_NilConfigs(t *testing.T) {
	// Both nil
	merged := MergeProcessors(nil, nil)
	if len(merged) != 0 {
		t.Errorf("MergeProcessors(nil, nil) returned %d processors, want 0", len(merged))
	}

	// Only global
	global := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Processors: []MessageProcessor{{Text: "GLOBAL"}},
		},
	}
	merged = MergeProcessors(global, nil)
	if len(merged) != 1 {
		t.Errorf("MergeProcessors(global, nil) returned %d processors, want 1", len(merged))
	}

	// Only workspace
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Processors: []MessageProcessor{{Text: "WORKSPACE"}},
		},
	}
	merged = MergeProcessors(nil, workspace)
	if len(merged) != 1 {
		t.Errorf("MergeProcessors(nil, workspace) returned %d processors, want 1", len(merged))
	}
}

func TestParse_ConversationsConfig(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  processing:
    override: false
    processors:
      - when:
          on: userPrompt
          match: first
        mutate: prepend
        text: "System prompt\n\n"
      - when:
          on: userPrompt
          match: all
        mutate: append
        text: "\n\n[Be concise]"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.Processing == nil {
		t.Fatal("Conversations.Processing is nil")
	}
	if cfg.Conversations.Processing.Override {
		t.Error("Override should be false")
	}
	if len(cfg.Conversations.Processing.Processors) != 2 {
		t.Fatalf("Processors count = %d, want 2", len(cfg.Conversations.Processing.Processors))
	}

	p0 := cfg.Conversations.Processing.Processors[0]
	if p0.When.On != ProcessorPhaseUserPrompt {
		t.Errorf("Processor[0].When.On = %q, want %q", p0.When.On, ProcessorPhaseUserPrompt)
	}
	if p0.When.Match != ProcessorMatchFirst {
		t.Errorf("Processor[0].When.Match = %q, want %q", p0.When.Match, ProcessorMatchFirst)
	}
	if p0.Mutate != ProcessorMutatePrepend {
		t.Errorf("Processor[0].Mutate = %q, want %q", p0.Mutate, ProcessorMutatePrepend)
	}
	if p0.Text != "System prompt\n\n" {
		t.Errorf("Processor[0].Text = %q, want %q", p0.Text, "System prompt\n\n")
	}

	p1 := cfg.Conversations.Processing.Processors[1]
	if p1.When.On != ProcessorPhaseUserPrompt {
		t.Errorf("Processor[1].When.On = %q, want %q", p1.When.On, ProcessorPhaseUserPrompt)
	}
	if p1.When.Match != ProcessorMatchAll {
		t.Errorf("Processor[1].When.Match = %q, want %q", p1.When.Match, ProcessorMatchAll)
	}
	if p1.Mutate != ProcessorMutateAppend {
		t.Errorf("Processor[1].Mutate = %q, want %q", p1.Mutate, ProcessorMutateAppend)
	}
}

func TestExternalImagesConfig(t *testing.T) {
	t.Run("nil config returns false", func(t *testing.T) {
		var nilConfig *ExternalImagesConfig
		if nilConfig.IsEnabled() {
			t.Error("nil config should return false")
		}
	})

	t.Run("nil Enabled returns false", func(t *testing.T) {
		cfg := &ExternalImagesConfig{Enabled: nil}
		if cfg.IsEnabled() {
			t.Error("nil Enabled should return false")
		}
	})

	t.Run("explicit false returns false", func(t *testing.T) {
		enabled := false
		cfg := &ExternalImagesConfig{Enabled: &enabled}
		if cfg.IsEnabled() {
			t.Error("explicit false should return false")
		}
	})

	t.Run("explicit true returns true", func(t *testing.T) {
		enabled := true
		cfg := &ExternalImagesConfig{Enabled: &enabled}
		if !cfg.IsEnabled() {
			t.Error("explicit true should return true")
		}
	})
}

func TestConversationsConfig_AreExternalImagesEnabled(t *testing.T) {
	t.Run("nil config returns false", func(t *testing.T) {
		var nilConfig *ConversationsConfig
		if nilConfig.AreExternalImagesEnabled() {
			t.Error("nil config should return false")
		}
	})

	t.Run("nil ExternalImages returns false", func(t *testing.T) {
		cfg := &ConversationsConfig{ExternalImages: nil}
		if cfg.AreExternalImagesEnabled() {
			t.Error("nil ExternalImages should return false")
		}
	})

	t.Run("enabled true returns true", func(t *testing.T) {
		enabled := true
		cfg := &ConversationsConfig{
			ExternalImages: &ExternalImagesConfig{Enabled: &enabled},
		}
		if !cfg.AreExternalImagesEnabled() {
			t.Error("enabled true should return true")
		}
	})
}

func TestParse_ExternalImagesConfig(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  external_images:
    enabled: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.ExternalImages == nil {
		t.Fatal("Conversations.ExternalImages is nil")
	}
	if !cfg.Conversations.ExternalImages.IsEnabled() {
		t.Error("ExternalImages.IsEnabled() should be true")
	}
	if !cfg.Conversations.AreExternalImagesEnabled() {
		t.Error("AreExternalImagesEnabled() should be true")
	}
}

func TestMergePrompts(t *testing.T) {
	// Note: globalFile prompts should have Source=PromptSourceFile set by ToWebPrompt()
	// when loaded from files. Here we simulate that.
	globalFile := []WebPrompt{
		{Name: "Global1", Prompt: "global1", Source: PromptSourceFile},
		{Name: "Shared", Prompt: "global-shared", Source: PromptSourceFile},
	}
	settings := []WebPrompt{
		{Name: "Settings1", Prompt: "settings1"},
		{Name: "Shared", Prompt: "settings-shared"},
	}
	workspace := []WebPrompt{
		{Name: "Workspace1", Prompt: "workspace1"},
		{Name: "Shared", Prompt: "workspace-shared"},
	}

	result := MergePrompts(globalFile, settings, workspace)

	// Should have 4 prompts: Workspace1, Shared (from workspace), Settings1, Global1
	if len(result) != 4 {
		t.Fatalf("len(result) = %d, want 4", len(result))
	}

	// Check order: workspace first, then settings, then global
	if result[0].Name != "Workspace1" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "Workspace1")
	}
	// Workspace prompts should have Source=PromptSourceWorkspace
	if result[0].Source != PromptSourceWorkspace {
		t.Errorf("result[0].Source = %q, want %q", result[0].Source, PromptSourceWorkspace)
	}

	if result[1].Name != "Shared" {
		t.Errorf("result[1].Name = %q, want %q", result[1].Name, "Shared")
	}
	// Shared should have workspace value (highest priority) and workspace source
	if result[1].Prompt != "workspace-shared" {
		t.Errorf("result[1].Prompt = %q, want %q", result[1].Prompt, "workspace-shared")
	}
	if result[1].Source != PromptSourceWorkspace {
		t.Errorf("result[1].Source = %q, want %q", result[1].Source, PromptSourceWorkspace)
	}

	if result[2].Name != "Settings1" {
		t.Errorf("result[2].Name = %q, want %q", result[2].Name, "Settings1")
	}
	// Settings prompts should have Source=PromptSourceSettings
	if result[2].Source != PromptSourceSettings {
		t.Errorf("result[2].Source = %q, want %q", result[2].Source, PromptSourceSettings)
	}

	if result[3].Name != "Global1" {
		t.Errorf("result[3].Name = %q, want %q", result[3].Name, "Global1")
	}
	// Global file prompts should retain Source=PromptSourceFile
	if result[3].Source != PromptSourceFile {
		t.Errorf("result[3].Source = %q, want %q", result[3].Source, PromptSourceFile)
	}
}

func TestMergePrompts_EmptyInputs(t *testing.T) {
	// All empty
	result := MergePrompts(nil, nil, nil)
	if len(result) != 0 {
		t.Errorf("MergePrompts(nil, nil, nil) = %d prompts, want 0", len(result))
	}

	// Only global
	global := []WebPrompt{{Name: "G1", Prompt: "g1"}}
	result = MergePrompts(global, nil, nil)
	if len(result) != 1 || result[0].Name != "G1" {
		t.Errorf("MergePrompts with only global failed")
	}

	// Only workspace
	workspace := []WebPrompt{{Name: "W1", Prompt: "w1"}}
	result = MergePrompts(nil, nil, workspace)
	if len(result) != 1 || result[0].Name != "W1" {
		t.Errorf("MergePrompts with only workspace failed")
	}
}

func TestMergePrompts_SkipsEmptyNames(t *testing.T) {
	prompts := []WebPrompt{
		{Name: "", Prompt: "no name"},
		{Name: "Valid", Prompt: "valid"},
	}

	result := MergePrompts(prompts, nil, nil)
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Name != "Valid" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "Valid")
	}
}

func TestMergePrompts_DisabledOverride(t *testing.T) {
	falseVal := false

	// Workspace disabled prompt should suppress same-named global prompt
	global := []WebPrompt{{Name: "Add tests", Prompt: "write tests"}}
	workspace := []WebPrompt{{Name: "Add tests", Enabled: &falseVal}}
	result := MergePrompts(global, nil, workspace)
	if len(result) != 0 {
		t.Errorf("expected 0 prompts (disabled override), got %d", len(result))
	}

	// Settings disabled prompt should suppress same-named global prompt
	settings := []WebPrompt{{Name: "Add tests", Enabled: &falseVal}}
	result = MergePrompts(global, settings, nil)
	if len(result) != 0 {
		t.Errorf("expected 0 prompts (settings disabled override), got %d", len(result))
	}

	// Non-disabled workspace prompt should still override global
	trueVal := true
	workspaceEnabled := []WebPrompt{{Name: "Add tests", Prompt: "workspace version", Enabled: &trueVal}}
	result = MergePrompts(global, nil, workspaceEnabled)
	if len(result) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(result))
	}
	if result[0].Prompt != "workspace version" {
		t.Errorf("expected workspace version, got %q", result[0].Prompt)
	}

	// Disabled prompt with nil Enabled (default true) should not be filtered
	defaultEnabled := []WebPrompt{{Name: "Add tests", Prompt: "default"}}
	result = MergePrompts(defaultEnabled, nil, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 prompt (nil Enabled = true), got %d", len(result))
	}
}

func TestParse_PermissionsConfig(t *testing.T) {
	yamlData := `
acp:
  - test:
      command: echo test
permissions:
  auto_approve: false
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Permissions == nil {
		t.Fatal("Permissions should not be nil")
	}
	if cfg.Permissions.AutoApprove == nil {
		t.Fatal("Permissions.AutoApprove should not be nil")
	}
	if *cfg.Permissions.AutoApprove != false {
		t.Error("Permissions.AutoApprove should be false")
	}
}

func TestParse_PermissionsConfigTrue(t *testing.T) {
	yamlData := `
acp:
  - test:
      command: echo test
permissions:
  auto_approve: true
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Permissions == nil {
		t.Fatal("Permissions should not be nil")
	}
	if cfg.Permissions.AutoApprove == nil {
		t.Fatal("Permissions.AutoApprove should not be nil")
	}
	if *cfg.Permissions.AutoApprove != true {
		t.Error("Permissions.AutoApprove should be true")
	}
}

func TestParse_PermissionsConfigDefault(t *testing.T) {
	// No permissions section - should default to auto_approve=true
	yamlData := `
acp:
  - test:
      command: echo test
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Permissions section should be nil when not specified
	if cfg.Permissions != nil {
		t.Fatal("Permissions should be nil when not specified")
	}

	// But IsAutoApprove should return true (the default)
	if !cfg.Permissions.IsAutoApprove() {
		t.Error("IsAutoApprove() should return true for nil Permissions")
	}
}

func TestPermissionsConfig_IsAutoApprove(t *testing.T) {
	tests := []struct {
		name     string
		config   *PermissionsConfig
		expected bool
	}{
		{
			name:     "nil config - defaults to true",
			config:   nil,
			expected: true,
		},
		{
			name:     "empty config - defaults to true",
			config:   &PermissionsConfig{},
			expected: true,
		},
		{
			name: "explicit true",
			config: &PermissionsConfig{
				AutoApprove: func() *bool { b := true; return &b }(),
			},
			expected: true,
		},
		{
			name: "explicit false",
			config: &PermissionsConfig{
				AutoApprove: func() *bool { b := false; return &b }(),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsAutoApprove()
			if result != tt.expected {
				t.Errorf("IsAutoApprove() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetMaxLoopIterations(t *testing.T) {
	t.Run("nil config returns default", func(t *testing.T) {
		var c *ConversationsConfig
		got := c.GetMaxLoopIterations()
		if got != DefaultMaxLoopIterations {
			t.Errorf("GetMaxLoopIterations() = %d, want %d", got, DefaultMaxLoopIterations)
		}
	})

	t.Run("nil field returns default", func(t *testing.T) {
		c := &ConversationsConfig{}
		got := c.GetMaxLoopIterations()
		if got != DefaultMaxLoopIterations {
			t.Errorf("GetMaxLoopIterations() = %d, want %d", got, DefaultMaxLoopIterations)
		}
	})

	t.Run("set value returned", func(t *testing.T) {
		v := 50
		c := &ConversationsConfig{MaxLoopIterations: &v}
		got := c.GetMaxLoopIterations()
		if got != 50 {
			t.Errorf("GetMaxLoopIterations() = %d, want 50", got)
		}
	})

	t.Run("value above backstop clamped to backstop", func(t *testing.T) {
		v := GlobalMaxLoopIterations + 500
		c := &ConversationsConfig{MaxLoopIterations: &v}
		got := c.GetMaxLoopIterations()
		if got != GlobalMaxLoopIterations {
			t.Errorf("GetMaxLoopIterations() = %d, want %d (backstop)", got, GlobalMaxLoopIterations)
		}
	})

	t.Run("zero returns zero (unlimited)", func(t *testing.T) {
		v := 0
		c := &ConversationsConfig{MaxLoopIterations: &v}
		got := c.GetMaxLoopIterations()
		if got != 0 {
			t.Errorf("GetMaxLoopIterations() = %d, want 0 (unlimited)", got)
		}
	})
}

func TestEffectiveMaxLoopIterations(t *testing.T) {
	tests := []struct {
		name      string
		promptMax int
		configMax int
		want      int
	}{
		{
			name:      "both zero → backstop",
			promptMax: 0,
			configMax: 0,
			want:      GlobalMaxLoopIterations,
		},
		{
			name:      "prompt cap wins (smallest positive)",
			promptMax: 5,
			configMax: 100,
			want:      5,
		},
		{
			name:      "config cap wins over large prompt cap",
			promptMax: 2000,
			configMax: 50,
			want:      50,
		},
		{
			// mitto-48x: promptMax=0 is an explicit author opt-out; configMax
			// (even when positive) does NOT bind — only the backstop applies.
			name:      "prompt zero opts out even when config is set",
			promptMax: 0,
			configMax: 200,
			want:      GlobalMaxLoopIterations,
		},
		{
			name:      "prompt cap wins when config is zero",
			promptMax: 10,
			configMax: 0,
			want:      10,
		},
		{
			name:      "both above backstop → backstop",
			promptMax: 1500,
			configMax: 2000,
			want:      GlobalMaxLoopIterations,
		},
		{
			name:      "prompt at backstop, config zero → backstop",
			promptMax: GlobalMaxLoopIterations,
			configMax: 0,
			want:      GlobalMaxLoopIterations,
		},
		// mitto-48x: prompt-declared maxIterations=0 means the author explicitly opted
		// out of any per-prompt cap ("standing supervisor, unlimited"). The config
		// default must NOT silently downgrade that to itself — only the hardcoded
		// GlobalMaxLoopIterations backstop applies. This test pins that contract and
		// is expected to fail against the current symmetric "smallest positive wins"
		// implementation, which returns configMax (100) here.
		{
			name:      "mitto-48x: prompt zero honored as unlimited (author opt-out, config default ignored)",
			promptMax: 0,
			configMax: 100,
			want:      GlobalMaxLoopIterations,
		},
		{
			name:      "mitto-48x: prompt zero honored as unlimited even when config is tiny",
			promptMax: 0,
			configMax: 5,
			want:      GlobalMaxLoopIterations,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveMaxLoopIterations(tt.promptMax, tt.configMax)
			if got != tt.want {
				t.Errorf("EffectiveMaxLoopIterations(%d, %d) = %d, want %d",
					tt.promptMax, tt.configMax, got, tt.want)
			}
		})
	}
}

func TestParse_MaxLoopIterations(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  max_loop_iterations: 42
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.MaxLoopIterations == nil {
		t.Fatal("MaxLoopIterations is nil, want 42")
	}
	if *cfg.Conversations.MaxLoopIterations != 42 {
		t.Errorf("MaxLoopIterations = %d, want 42", *cfg.Conversations.MaxLoopIterations)
	}
	if cfg.Conversations.GetMaxLoopIterations() != 42 {
		t.Errorf("GetMaxLoopIterations() = %d, want 42", cfg.Conversations.GetMaxLoopIterations())
	}
}

func TestParse_MaxLoopIterations_Zero(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  max_loop_iterations: 0
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.MaxLoopIterations == nil {
		t.Fatal("MaxLoopIterations is nil, want 0")
	}
	if *cfg.Conversations.MaxLoopIterations != 0 {
		t.Errorf("MaxLoopIterations = %d, want 0", *cfg.Conversations.MaxLoopIterations)
	}
	if cfg.Conversations.GetMaxLoopIterations() != 0 {
		t.Errorf("GetMaxLoopIterations() = %d, want 0 (unlimited)", cfg.Conversations.GetMaxLoopIterations())
	}
}

func TestGetMinLoopCompletionDelaySeconds(t *testing.T) {
	t.Run("nil config returns default", func(t *testing.T) {
		var c *ConversationsConfig
		got := c.GetMinLoopCompletionDelaySeconds()
		if got != DefaultMinLoopCompletionDelaySeconds {
			t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want %d", got, DefaultMinLoopCompletionDelaySeconds)
		}
	})

	t.Run("nil field returns default", func(t *testing.T) {
		c := &ConversationsConfig{}
		got := c.GetMinLoopCompletionDelaySeconds()
		if got != DefaultMinLoopCompletionDelaySeconds {
			t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want %d", got, DefaultMinLoopCompletionDelaySeconds)
		}
	})

	t.Run("set value returned", func(t *testing.T) {
		v := 10
		c := &ConversationsConfig{MinLoopCompletionDelaySeconds: &v}
		got := c.GetMinLoopCompletionDelaySeconds()
		if got != 10 {
			t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want 10", got)
		}
	})

	t.Run("negative value treated as zero", func(t *testing.T) {
		v := -3
		c := &ConversationsConfig{MinLoopCompletionDelaySeconds: &v}
		got := c.GetMinLoopCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want 0 (negative → 0)", got)
		}
	})

	t.Run("zero is valid (no floor)", func(t *testing.T) {
		v := 0
		c := &ConversationsConfig{MinLoopCompletionDelaySeconds: &v}
		got := c.GetMinLoopCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want 0", got)
		}
	})
}

func TestParse_MinLoopCompletionDelaySeconds(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  min_loop_completion_delay_seconds: 10
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.MinLoopCompletionDelaySeconds == nil {
		t.Fatal("MinLoopCompletionDelaySeconds is nil, want 10")
	}
	if *cfg.Conversations.MinLoopCompletionDelaySeconds != 10 {
		t.Errorf("MinLoopCompletionDelaySeconds = %d, want 10", *cfg.Conversations.MinLoopCompletionDelaySeconds)
	}
	if cfg.Conversations.GetMinLoopCompletionDelaySeconds() != 10 {
		t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want 10", cfg.Conversations.GetMinLoopCompletionDelaySeconds())
	}
}

func TestParse_Models(t *testing.T) {
	yaml := `
models:
  - name: Opus
    criteria:
      matchMode: contains
      pattern: Opus
    tags: [Smartest, Expensive]
  - name: Sonnet
    criteria:
      matchMode: contains
      pattern: Sonnet
    tags: [Smart, Cheap]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Models) != 2 {
		t.Fatalf("Models count = %d, want 2", len(cfg.Models))
	}

	opus := cfg.Models[0]
	if opus.Name != "Opus" {
		t.Errorf("Models[0].Name = %q, want %q", opus.Name, "Opus")
	}
	if opus.Criteria == nil {
		t.Fatalf("Models[0].Criteria is nil, want non-nil")
	}
	if opus.Criteria.MatchMode != "contains" {
		t.Errorf("Models[0].Criteria.MatchMode = %q, want %q", opus.Criteria.MatchMode, "contains")
	}
	if opus.Criteria.Pattern != "Opus" {
		t.Errorf("Models[0].Criteria.Pattern = %q, want %q", opus.Criteria.Pattern, "Opus")
	}
	if len(opus.Tags) != 2 || opus.Tags[0] != "Smartest" || opus.Tags[1] != "Expensive" {
		t.Errorf("Models[0].Tags = %v, want [Smartest Expensive]", opus.Tags)
	}

	sonnet := cfg.Models[1]
	if sonnet.Name != "Sonnet" {
		t.Errorf("Models[1].Name = %q, want %q", sonnet.Name, "Sonnet")
	}
	if len(sonnet.Tags) != 2 || sonnet.Tags[0] != "Smart" || sonnet.Tags[1] != "Cheap" {
		t.Errorf("Models[1].Tags = %v, want [Smart Cheap]", sonnet.Tags)
	}
}

func TestParse_ModelsEmptyAndNameless(t *testing.T) {
	// A profile without a name must be skipped; a profile without criteria is allowed.
	yaml := `
models:
  - name: ""
    tags: [ignored]
  - name: TagsOnly
    tags: [Fast]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("Models count = %d, want 1", len(cfg.Models))
	}
	if cfg.Models[0].Name != "TagsOnly" {
		t.Errorf("Models[0].Name = %q, want %q", cfg.Models[0].Name, "TagsOnly")
	}
	if cfg.Models[0].Criteria != nil {
		t.Errorf("Models[0].Criteria = %v, want nil", cfg.Models[0].Criteria)
	}
}

// TestConstraintMatchesName pins the single-string match engine shared by
// MatchConstraintOption and model-tag resolution across all match modes.
func TestConstraintMatchesName(t *testing.T) {
	tests := []struct {
		name       string
		constraint *ACPServerConstraint
		input      string
		want       bool
	}{
		{name: "nil constraint never matches", constraint: nil, input: "Opus 4.8", want: false},
		{name: "contains hit", constraint: &ACPServerConstraint{MatchMode: "contains", Pattern: "opus"}, input: "Opus 4.8", want: true},
		{name: "contains case insensitive", constraint: &ACPServerConstraint{MatchMode: "contains", Pattern: "OPUS"}, input: "opus-4.8", want: true},
		{name: "contains miss", constraint: &ACPServerConstraint{MatchMode: "contains", Pattern: "sonnet"}, input: "Opus 4.8", want: false},
		{name: "exact hit case insensitive", constraint: &ACPServerConstraint{MatchMode: "exact", Pattern: "gpt-4o"}, input: "GPT-4o", want: true},
		{name: "exact miss for partial", constraint: &ACPServerConstraint{MatchMode: "exact", Pattern: "opus"}, input: "opus-4.8", want: false},
		{name: "startsWith hit", constraint: &ACPServerConstraint{MatchMode: "startsWith", Pattern: "opus"}, input: "Opus 4.8", want: true},
		{name: "startsWith miss", constraint: &ACPServerConstraint{MatchMode: "startsWith", Pattern: "4.8"}, input: "Opus 4.8", want: false},
		{name: "regex hit case insensitive", constraint: &ACPServerConstraint{MatchMode: "regex", Pattern: "opus-4\\.[78]"}, input: "OPUS-4.8", want: true},
		{name: "regex miss", constraint: &ACPServerConstraint{MatchMode: "regex", Pattern: "^claude"}, input: "Opus 4.8", want: false},
		{name: "lookAlike all words present", constraint: &ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}, input: "opus-4.8", want: true},
		{name: "lookAlike word missing", constraint: &ACPServerConstraint{MatchMode: "lookAlike", Pattern: "opus 5.0"}, input: "opus-4.8", want: false},
		{name: "lookAlike empty pattern", constraint: &ACPServerConstraint{MatchMode: "lookAlike", Pattern: ""}, input: "opus-4.8", want: false},
		{name: "unknown mode", constraint: &ACPServerConstraint{MatchMode: "nope", Pattern: "opus"}, input: "opus-4.8", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConstraintMatchesName(tt.constraint, tt.input); got != tt.want {
				t.Errorf("ConstraintMatchesName() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestModelProfileByName covers case-insensitive profile lookup by name.
func TestModelProfileByName(t *testing.T) {
	cfg := &Config{Models: []ModelProfile{
		{Name: "Opus", Tags: []string{"Smartest"}},
		{Name: "Sonnet", Tags: []string{"Smart"}},
	}}

	p, ok := cfg.ModelProfileByName("opus")
	if !ok {
		t.Fatalf("ModelProfileByName(opus) ok = false, want true")
	}
	if p.Name != "Opus" {
		t.Errorf("ModelProfileByName(opus).Name = %q, want %q", p.Name, "Opus")
	}

	if _, ok := cfg.ModelProfileByName("haiku"); ok {
		t.Errorf("ModelProfileByName(haiku) ok = true, want false")
	}
}

// TestModelProfilesByTag covers case-insensitive tag filtering, including a tag shared
// by multiple profiles. It exercises the pure slice-based engine (ProfilesByTag) so it
// stays free of the canonical-default merge that (*Config).ModelProfilesByTag applies;
// the merge itself is covered by TestEffectiveModelProfiles_MergeAndPrecedence.
func TestModelProfilesByTag(t *testing.T) {
	profiles := []ModelProfile{
		{Name: "Opus", Tags: []string{"Smartest", "Expensive"}},
		{Name: "Sonnet", Tags: []string{"Smart", "Cheap"}},
		{Name: "Haiku", Tags: []string{"Fast", "Cheap"}},
	}

	cheap := ProfilesByTag(profiles, "cheap")
	if len(cheap) != 2 {
		t.Fatalf("ProfilesByTag(cheap) count = %d, want 2", len(cheap))
	}
	if cheap[0].Name != "Sonnet" || cheap[1].Name != "Haiku" {
		t.Errorf("ProfilesByTag(cheap) = [%s %s], want [Sonnet Haiku]", cheap[0].Name, cheap[1].Name)
	}

	if got := ProfilesByTag(profiles, "missing"); len(got) != 0 {
		t.Errorf("ProfilesByTag(missing) count = %d, want 0", len(got))
	}
}

// TestResolveModelTags covers tag resolution across every match mode, the union (with
// case-insensitive de-dup) across multiple matching profiles, the no-match / empty cases,
// and that criteria-less profiles never contribute tags. It exercises the pure slice-based
// core (resolveModelTags) so it stays free of the canonical-default merge that
// (*Config).ResolveModelTags applies; the merge is covered elsewhere.
func TestResolveModelTags(t *testing.T) {
	profiles := []ModelProfile{
		{Name: "Opus", Criteria: &ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}, Tags: []string{"Smart", "Expensive"}},
		{Name: "Claude", Criteria: &ACPServerConstraint{MatchMode: "regex", Pattern: "opus|sonnet"}, Tags: []string{"Anthropic", "smart"}},
		{Name: "Sonnet", Criteria: &ACPServerConstraint{MatchMode: "exact", Pattern: "Sonnet 4.6"}, Tags: []string{"Cheap"}},
		{Name: "Pro", Criteria: &ACPServerConstraint{MatchMode: "startsWith", Pattern: "opus"}, Tags: []string{"Pro"}},
		{Name: "Look", Criteria: &ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}, Tags: []string{"Latest"}},
		{Name: "TagsOnly", Tags: []string{"NeverApplied"}}, // nil criteria → never matches
	}

	tests := []struct {
		name      string
		modelName string
		want      []string
	}{
		// "Opus 4.8" matches Opus (contains), Claude (regex), Pro (startsWith), Look (lookAlike).
		// Union with case-insensitive de-dup: Smart, Expensive, Anthropic, Pro, Latest
		// ("smart" from Claude is dropped as a dup of "Smart").
		{name: "union across modes", modelName: "Opus 4.8", want: []string{"Smart", "Expensive", "Anthropic", "Pro", "Latest"}},
		{name: "exact only", modelName: "Sonnet 4.6", want: []string{"Anthropic", "smart", "Cheap"}},
		{name: "no match", modelName: "GPT-4o", want: nil},
		{name: "empty name", modelName: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.modelName == "" {
				if got := (&Config{Models: profiles}).ResolveModelTags(""); got != nil {
					t.Fatalf("ResolveModelTags(\"\") = %v, want nil", got)
				}
				return
			}
			got := resolveModelTags(profiles, tt.modelName)
			if len(got) != len(tt.want) {
				t.Fatalf("ResolveModelTags(%q) = %v, want %v", tt.modelName, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("ResolveModelTags(%q)[%d] = %q, want %q", tt.modelName, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParse_EmbeddedDefaultModelProfiles pins the well-known model profiles seeded into
// new installs via the embedded config/config.default.yaml. It guards against the shipped
// default drifting (bad YAML, renamed profiles, or dropped tags) and verifies that the
// union resolution behaves as documented for representative model names.
func TestParse_EmbeddedDefaultModelProfiles(t *testing.T) {
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		t.Fatalf("Parse(embedded default) failed: %v", err)
	}

	wantProfiles := map[string][]string{
		"Claude":          {"Anthropic"},
		"Claude Mythos":   {"Smartest", "Reasoning", "Thinking", "Deep", "Slow", "Expensive"},
		"Claude Opus":     {"Smartest", "Reasoning", "Thinking", "Deep", "Slow", "Expensive"},
		"Claude Sonnet 5": {"Smart", "Coding"},
		"Claude Sonnet 4": {"Smart", "Coding"},
		"Claude Haiku":    {"Fast", "Cheap"},
		"GPT-5":           {"Smart", "Reasoning", "Thinking", "Deep", "Coding"},
		"GPT-4":           {"Smart", "Coding"},
		"OpenAI GPT":      {"OpenAI"},
		"Gemini":          {"Smart", "LongContext"},
		"GLM":             {"Smart", "Coding", "OpenWeight", "SelfHostable"},
		"DeepSeek":        {"Smart", "Coding", "OpenWeight", "SelfHostable"},
	}

	if len(cfg.Models) != len(wantProfiles) {
		t.Fatalf("embedded default Models count = %d, want %d", len(cfg.Models), len(wantProfiles))
	}

	for name, wantTags := range wantProfiles {
		p, ok := cfg.ModelProfileByName(name)
		if !ok {
			t.Errorf("embedded default missing profile %q", name)
			continue
		}
		if p.Criteria == nil || p.Criteria.MatchMode != "contains" {
			t.Errorf("profile %q criteria = %+v, want matchMode contains", name, p.Criteria)
		}
		if len(p.Tags) != len(wantTags) {
			t.Errorf("profile %q tags = %v, want %v", name, p.Tags, wantTags)
			continue
		}
		for i := range wantTags {
			if p.Tags[i] != wantTags[i] {
				t.Errorf("profile %q tags[%d] = %q, want %q", name, i, p.Tags[i], wantTags[i])
			}
		}
	}

	// "Claude Opus 4.x" matches the vendor-level Claude (contains "Claude") and the Opus
	// profile (contains "Opus"); the union de-dups case-insensitively.
	opusTags := cfg.ResolveModelTags("Claude Opus 4.5")
	wantOpus := []string{"Anthropic", "Smartest", "Reasoning", "Thinking", "Deep", "Slow", "Expensive"}
	if len(opusTags) != len(wantOpus) {
		t.Fatalf("ResolveModelTags(Claude Opus 4.5) = %v, want %v", opusTags, wantOpus)
	}
	for i := range wantOpus {
		if opusTags[i] != wantOpus[i] {
			t.Errorf("ResolveModelTags(Claude Opus 4.5)[%d] = %q, want %q", i, opusTags[i], wantOpus[i])
		}
	}

	// A non-Anthropic model only picks up its own profile's tags.
	if got := cfg.ResolveModelTags("Gemini 2.5 Pro"); len(got) != 2 || got[0] != "Smart" || got[1] != "LongContext" {
		t.Errorf("ResolveModelTags(Gemini 2.5 Pro) = %v, want [Smart LongContext]", got)
	}
}

// TestParse_EmbeddedDefaultShortcuts pins the safe default global shortcuts
// seeded into new installs via the embedded config/config.default.yaml. It
// guards against the shipped defaults drifting (bad YAML, renamed sections, or
// dropped buttons) so first-time users always get working shortcut buttons.
func TestParse_EmbeddedDefaultShortcuts(t *testing.T) {
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		t.Fatalf("Parse(embedded default) failed: %v", err)
	}

	want := map[string]string{
		"conversations": "Commit changes",
		"beadsIssue":    "Start work",
		"tasksList":     "Overview",
	}

	if len(cfg.Shortcuts) != len(want) {
		t.Fatalf("embedded default Shortcuts sections = %d, want %d (%v)", len(cfg.Shortcuts), len(want), cfg.Shortcuts)
	}
	for section, wantPrompt := range want {
		buttons, ok := cfg.Shortcuts[section]
		if !ok {
			t.Errorf("embedded default missing shortcuts section %q", section)
			continue
		}
		if len(buttons) != 1 {
			t.Errorf("section %q buttons = %d, want 1", section, len(buttons))
			continue
		}
		if buttons[0].Prompt != wantPrompt {
			t.Errorf("section %q prompt = %q, want %q", section, buttons[0].Prompt, wantPrompt)
		}
	}
}

// TestDefaultModelProfiles_MatchesEmbeddedYAML asserts the hardcoded Go source of
// truth (DefaultModelProfiles) stays in sync with the shipped config.default.yaml
// `models:` block — same profile names, criteria, and tags in the same order. This is
// the drift guard invoked by `make check-model-tags`.
func TestDefaultModelProfiles_MatchesEmbeddedYAML(t *testing.T) {
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		t.Fatalf("Parse(embedded default) failed: %v", err)
	}
	got := DefaultModelProfiles()
	if len(got) != len(cfg.Models) {
		t.Fatalf("DefaultModelProfiles() count = %d, config.default.yaml models = %d", len(got), len(cfg.Models))
	}
	for i := range got {
		g, y := got[i], cfg.Models[i]
		if g.Name != y.Name {
			t.Errorf("profile[%d] name = %q (Go) vs %q (YAML)", i, g.Name, y.Name)
		}
		if g.Criteria == nil || y.Criteria == nil {
			t.Errorf("profile[%d] %q missing criteria (Go=%v YAML=%v)", i, g.Name, g.Criteria, y.Criteria)
			continue
		}
		if g.Criteria.MatchMode != y.Criteria.MatchMode || g.Criteria.Pattern != y.Criteria.Pattern {
			t.Errorf("profile[%d] %q criteria = %+v (Go) vs %+v (YAML)", i, g.Name, g.Criteria, y.Criteria)
		}
		if strings.Join(g.Tags, ",") != strings.Join(y.Tags, ",") {
			t.Errorf("profile[%d] %q tags = %v (Go) vs %v (YAML)", i, g.Name, g.Tags, y.Tags)
		}
	}
}

// TestCanonicalModelTags pins the canonical capability-tag set (sorted, de-duplicated)
// derived from DefaultModelProfiles.
func TestCanonicalModelTags(t *testing.T) {
	want := []string{"Anthropic", "Cheap", "Coding", "Deep", "Expensive", "Fast", "LongContext", "OpenAI", "OpenWeight", "Reasoning", "SelfHostable", "Slow", "Smart", "Smartest", "Thinking"}
	got := CanonicalModelTags()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CanonicalModelTags() = %v, want %v", got, want)
	}
}

// TestEffectiveModelProfiles_MergeAndPrecedence verifies that user-configured profiles
// win on name collision and canonical defaults fill the gaps, including the empty and
// nil-Config cases (the exact scenario behind tag routing silently no-oping when
// settings.json omits `models:`).
func TestEffectiveModelProfiles_MergeAndPrecedence(t *testing.T) {
	// Nil Config → all canonical defaults.
	var nilCfg *Config
	if got := nilCfg.EffectiveModelProfiles(); len(got) != len(DefaultModelProfiles()) {
		t.Fatalf("nil Config EffectiveModelProfiles() = %d, want %d", len(got), len(DefaultModelProfiles()))
	}

	// Empty Models → all canonical defaults, and a tag resolves.
	empty := &Config{}
	if got := empty.ModelProfilesByTag("Coding"); len(got) == 0 {
		t.Errorf("empty Config: modelTag Coding resolved to no profile (regression: routing no-ops)")
	}

	// User override on a colliding name wins; a non-colliding user profile is preserved;
	// defaults fill the rest.
	user := &Config{Models: []ModelProfile{
		{Name: "Claude Sonnet 4", Criteria: &ACPServerConstraint{MatchMode: "exact", Pattern: "My Sonnet"}, Tags: []string{"Custom"}},
		{Name: "MyLocal", Criteria: &ACPServerConstraint{MatchMode: "contains", Pattern: "local"}, Tags: []string{"Cheap"}},
	}}
	eff := user.EffectiveModelProfiles()
	// User profiles come first, in order.
	if eff[0].Name != "Claude Sonnet 4" || eff[0].Criteria.Pattern != "My Sonnet" || eff[0].Tags[0] != "Custom" {
		t.Errorf("user override not preserved/first: %+v", eff[0])
	}
	if eff[1].Name != "MyLocal" {
		t.Errorf("non-colliding user profile not preserved at index 1: %+v", eff[1])
	}
	// The colliding default (Claude Sonnet 4) must NOT be appended again.
	sonnetCount := 0
	for _, p := range eff {
		if p.Name == "Claude Sonnet 4" {
			sonnetCount++
		}
	}
	if sonnetCount != 1 {
		t.Errorf("Claude Sonnet 4 appears %d times, want 1 (default should be dropped on collision)", sonnetCount)
	}
	// A default with a unique name (e.g. Claude Opus) is still present.
	if p, ok := user.ModelProfileByName("Claude Opus"); !ok || p == nil {
		t.Errorf("canonical default 'Claude Opus' missing after merge")
	}
}

// TestBuiltinPrompts_ModelTagsAreCanonical is the validator behind `make check-model-tags`:
// every `modelTag:` used by any embedded builtin prompt must be a known canonical tag.
// This fails CI if a prompt references a tag that no model profile can carry.
func TestBuiltinPrompts_ModelTagsAreCanonical(t *testing.T) {
	canonical := make(map[string]struct{})
	for _, tag := range CanonicalModelTags() {
		canonical[strings.ToLower(tag)] = struct{}{}
	}

	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "name" . }}` refs at parse-time precompile (mitto-g61.4).
	// Restored on cleanup so nil-baseline tests remain unaffected.
	builtinDiskDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDiskDir); err == nil {
		prev := CurrentFragments()
		t.Cleanup(func() { SetCurrentFragments(prev) })
		reg, loadErrs, ferr := LoadFragmentsFromDir(builtinDiskDir)
		if ferr != nil {
			t.Fatalf("LoadFragmentsFromDir(builtin): %v", ferr)
		}
		if len(loadErrs) != 0 {
			t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
		}
		SetCurrentFragments(reg)
	}

	// Walk the embedded builtin prompts recursively so nested subgroups
	// (Phase B of mitto-j88) are validated too, not just the flat top level.
	var unknown []string
	var walked int
	walkErr := fs.WalkDir(defaultConfig.BuiltinPromptsFS, defaultConfig.BuiltinPromptsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".prompt.yaml") {
			return nil
		}
		walked++
		data, err := fs.ReadFile(defaultConfig.BuiltinPromptsFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		pf, err := ParsePromptFile(path, data, time.Time{})
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, pm := range pf.PreferredModels {
			if pm.ModelTag == "" {
				continue
			}
			if _, ok := canonical[strings.ToLower(pm.ModelTag)]; !ok {
				unknown = append(unknown, path+": "+pm.ModelTag)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk embedded builtin prompts: %v", walkErr)
	}
	if walked == 0 {
		t.Fatal("no embedded builtin prompts found")
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		t.Fatalf("builtin prompts reference unknown modelTag(s) not in CanonicalModelTags():\n  %s",
			strings.Join(unknown, "\n  "))
	}
}

// -----------------------------------------------------------------------------
// StatsConfig YAML wiring (mitto-a86b.9)
// -----------------------------------------------------------------------------

func TestLoad_StatsRetentionHours_YAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".mittorc")

	// Overrides stats.retention_hours = 24 → the acceptance-criterion #2
	// wiring for the retention worker. Explicit 0 must survive as
	// "disable pruning" (distinct from unset → default 90 d).
	yaml := `
acp:
  - test:
      command: "test-cmd"
stats:
  retention_hours: 24
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Stats == nil {
		t.Fatal("cfg.Stats is nil, want populated StatsConfig")
	}
	if cfg.Stats.GetRetentionHours() != 24 {
		t.Errorf("GetRetentionHours() = %d, want 24 (from stats.retention_hours override)",
			cfg.Stats.GetRetentionHours())
	}
	if got := cfg.Stats.GetRetention(); got != 24*time.Hour {
		t.Errorf("GetRetention() = %v, want %v", got, 24*time.Hour)
	}
}

func TestLoad_StatsRetentionHours_UnsetGivesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".mittorc")

	yaml := `
acp:
  - test:
      command: "test-cmd"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// cfg.Stats may be nil when the yaml omits the section entirely; the
	// nil-safe getter still returns the default (90 d).
	if got := cfg.Stats.GetRetentionHours(); got != DefaultStatsRetentionHours {
		t.Errorf("unset stats.GetRetentionHours() = %d, want %d",
			got, DefaultStatsRetentionHours)
	}
}

func TestLoad_StatsRetentionHours_ExplicitZeroDisables(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".mittorc")

	yaml := `
acp:
  - test:
      command: "test-cmd"
stats:
  retention_hours: 0
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Stats == nil || cfg.Stats.RetentionHours == nil {
		t.Fatal("explicit stats.retention_hours=0 was not preserved (must be distinct from unset)")
	}
	if got := cfg.Stats.GetRetentionHours(); got != 0 {
		t.Errorf("explicit 0 got %d, want 0 (disable pruning)", got)
	}
	if got := cfg.Stats.GetRetention(); got != 0 {
		t.Errorf("explicit 0 GetRetention() = %v, want 0", got)
	}
}

// TestParse_ConversationFont covers the mitto-9tl "Conversation font settings
// group" additions to WebUIConfig: two new keys must round-trip through the
// YAML load path (raw struct + populate block) alongside the sibling input-font
// keys and must remain empty strings (not defaulted here) when the caller
// omits them — matching how input_font_family / input_font_size behave.
func TestParse_ConversationFont_Set(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  web:
    conversation_font_family: "inter"
    conversation_font_size: "lg"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Web == nil {
		t.Fatal("UI.Web is nil, want populated struct")
	}

	if got, want := cfg.UI.Web.ConversationFontFamily, "inter"; got != want {
		t.Errorf("ConversationFontFamily = %q, want %q", got, want)
	}
	if got, want := cfg.UI.Web.ConversationFontSize, "lg"; got != want {
		t.Errorf("ConversationFontSize = %q, want %q", got, want)
	}
}

// TestParse_ConversationFont_Empty asserts that when the caller does not
// provide the two conversation-font keys, they remain empty strings (the
// frontend applies the "system" / "sm" defaults). This matches the behavior
// of the sibling InputFontFamily/InputFontSize fields.
func TestParse_ConversationFont_Empty(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  web:
    input_font_family: "system"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Web == nil {
		t.Fatal("UI.Web is nil, want populated struct (input_font_family is set)")
	}
	if got := cfg.UI.Web.ConversationFontFamily; got != "" {
		t.Errorf("ConversationFontFamily = %q, want empty string (no config-side default)", got)
	}
	if got := cfg.UI.Web.ConversationFontSize; got != "" {
		t.Errorf("ConversationFontSize = %q, want empty string (no config-side default)", got)
	}
}

// TestParse_ConversationFont_PreservedAlongsideInputFont exercises the raw
// YAML struct + populate block together: all four web font keys must survive
// the load cycle in the same call. This is the regression test that would
// have caught forgetting to add the new fields to either the raw block
// (silently dropped) or the populate block (parsed but not copied).
func TestParse_ConversationFont_PreservedAlongsideInputFont(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  web:
    input_font_family: "menlo"
    input_font_size: "large"
    conversation_font_family: "georgia"
    conversation_font_size: "md"
    conversation_cycling_mode: "all"
    single_expanded_group: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Web == nil {
		t.Fatal("UI.Web is nil")
	}

	checks := []struct {
		field, got, want string
	}{
		{"InputFontFamily", cfg.UI.Web.InputFontFamily, "menlo"},
		{"InputFontSize", cfg.UI.Web.InputFontSize, "large"},
		{"ConversationFontFamily", cfg.UI.Web.ConversationFontFamily, "georgia"},
		{"ConversationFontSize", cfg.UI.Web.ConversationFontSize, "md"},
		{"ConversationCyclingMode", cfg.UI.Web.ConversationCyclingMode, "all"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if !cfg.UI.Web.SingleExpandedGroup {
		t.Errorf("SingleExpandedGroup = false, want true")
	}
}
