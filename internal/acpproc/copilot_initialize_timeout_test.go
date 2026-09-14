package acpproc

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// This file reproduces mitto-sbj: GitHub Copilot `--acp` fails to start
// (failed_to_start) because `copilot --acp` connects ALL configured MCP
// servers BEFORE it answers the ACP `initialize` RPC. When that pre-handshake
// MCP-connect phase exceeds Mitto's fixed per-attempt Initialize deadline
// (processInitializeAttemptTimeout = 25s, doStartProcess), Mitto SIGKILLs the
// still-starting copilot process, retries 3x (each redoing the full cold MCP
// connect), and gives up with a -32603 "context deadline exceeded" /
// session_end reason=failed_to_start.
//
// Both tests below are deterministic and fast (no process spawn, no sleep):
// they assert the two concrete artifacts a per-agent Initialize-timeout
// override needs, and both currently FAIL because neither exists yet. See
// the "Investigation" comment on mitto-sbj for the full wiring plan
// (mirrors the existing AgentDefaultEnv feature, mitto-6dur).

// TestSharedACPProcessConfig_SupportsPerAgentInitializeTimeoutOverride
// reproduces mitto-sbj at the config-carrier layer: SharedACPProcessConfig
// has no field letting a caller widen the per-attempt Initialize deadline
// for a specific agent. Without it, doStartProcess unconditionally uses the
// fixed processInitializeAttemptTimeout for every agent, including ones
// (like github-copilot) that front-load MCP connections before handshaking.
//
// Fails today (no such field exists). Will pass once a
// time.Duration-typed override field (e.g. InitializeTimeout) is added to
// SharedACPProcessConfig and honored by doStartProcess when > 0.
func TestSharedACPProcessConfig_SupportsPerAgentInitializeTimeoutOverride(t *testing.T) {
	field, ok := reflect.TypeOf(SharedACPProcessConfig{}).FieldByName("InitializeTimeout")
	if !ok {
		t.Fatalf("mitto-sbj: SharedACPProcessConfig has no InitializeTimeout override field; "+
			"agents that front-load MCP connections before the ACP handshake (e.g. github-copilot) "+
			"cannot get a longer per-attempt Initialize deadline than the fixed "+
			"processInitializeAttemptTimeout (%v), so a slow cold MCP-connect phase hard-fails the "+
			"conversation with failed_to_start", processInitializeAttemptTimeout)
	}
	if field.Type != reflect.TypeOf(time.Duration(0)) {
		t.Errorf("mitto-sbj: SharedACPProcessConfig.InitializeTimeout has type %s; want time.Duration",
			field.Type)
	}
}

// copilotMetadataDefaultsProbe mirrors just enough of the github-copilot
// metadata.yaml defaults block to check for an initializeTimeout override,
// independent of the production agents.AgentDefaults schema (so this test
// does not require any production struct change to compile).
type copilotMetadataDefaultsProbe struct {
	Defaults struct {
		InitializeTimeout string `yaml:"initializeTimeout"`
	} `yaml:"defaults"`
}

// TestGitHubCopilotMetadata_DeclaresInitializeTimeoutOverride reproduces
// mitto-sbj at the agent-declaration layer: the builtin github-copilot
// metadata.yaml declares no initializeTimeout default, even though copilot's
// startup ordering (MCP connect before ACP handshake) needs one bigger than
// Mitto's default processInitializeAttemptTimeout.
//
// Fails today (no defaults.initializeTimeout key, or a value that does not
// exceed processInitializeAttemptTimeout). Will pass once
// config/agents/builtin/github-copilot/metadata.yaml declares
// defaults.initializeTimeout with a duration greater than
// processInitializeAttemptTimeout (e.g. "90s").
func TestGitHubCopilotMetadata_DeclaresInitializeTimeoutOverride(t *testing.T) {
	metaPath := filepath.Join(builtinAgentDirForACPProcTest(t), "github-copilot", "metadata.yaml")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", metaPath, err)
	}

	var probe copilotMetadataDefaultsProbe
	if err := yaml.Unmarshal(data, &probe); err != nil {
		t.Fatalf("cannot parse %s: %v", metaPath, err)
	}

	if probe.Defaults.InitializeTimeout == "" {
		t.Fatalf("mitto-sbj: %s has no defaults.initializeTimeout entry; github-copilot connects "+
			"all MCP servers before answering the ACP `initialize` RPC, so it needs a longer "+
			"per-attempt deadline than the default processInitializeAttemptTimeout (%v)",
			metaPath, processInitializeAttemptTimeout)
	}

	d, err := time.ParseDuration(probe.Defaults.InitializeTimeout)
	if err != nil {
		t.Fatalf("mitto-sbj: %s defaults.initializeTimeout=%q is not a valid duration: %v",
			metaPath, probe.Defaults.InitializeTimeout, err)
	}
	if d <= processInitializeAttemptTimeout {
		t.Errorf("mitto-sbj: %s defaults.initializeTimeout=%v must exceed "+
			"processInitializeAttemptTimeout=%v to actually help copilot's cold MCP-connect-before-handshake "+
			"ordering", metaPath, d, processInitializeAttemptTimeout)
	}
}

// builtinAgentDirForACPProcTest returns the absolute path to
// config/agents/builtin, relative to this test file's own location, so the
// test works regardless of the current working directory. Mirrors
// builtinAgentsDirForTest in internal/agents/stderr_patterns_test.go.
func builtinAgentDirForACPProcTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/acpproc/copilot_initialize_timeout_test.go
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(repoRoot, "config", "agents", "builtin")
}
