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

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// TestHandleConfirmAgents_ExistingEnvlessServer_NeverBackfillsNodeOptions
// reproduces mitto-qphs: an ACP subprocess V8 heap OOM (SIGABRT) because the
// agent's intended --max-old-space-size default never reaches the subprocess
// for an ACP server entry that already exists in settings.json with an empty
// Env map.
//
// Root cause (see the Investigation comment on mitto-qphs): seedACPServerDefaults
// (agent_discovery.go) only seeds AgentDefaults.Env onto a *newly created*
// ACPServerSettings entry inside HandleConfirmAgents. But HandleConfirmAgents
// itself skips any agent whose name already exists in settings.ACPServers
// ("Build a set of existing server names to avoid duplicates") *before*
// seeding is ever attempted — so a pre-existing, env-less entry (e.g. created
// before the agent's metadata.yaml gained a NODE_OPTIONS default, or added via
// a path that didn't pass dir_name) is never revisited and never backfilled.
// There is no other path in the codebase that re-seeds defaults onto an
// existing ACPServerSettings entry.
//
// This test confirms the currently-missing behavior: re-confirming an agent
// that already has a settings.json entry with an empty Env should backfill
// the agent's defaults.env.NODE_OPTIONS (raising the V8 heap cap), matching
// what a brand-new entry gets. Today it does not, and the entry is left with
// no NODE_OPTIONS at all — the process falls back to V8's own ~6 GiB default,
// which is exactly the crash observed in mitto-qphs.
func TestHandleConfirmAgents_ExistingEnvlessServer_NeverBackfillsNodeOptions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Pre-existing settings.json: an ACP server entry for "augment" that
	// predates (or otherwise missed) NODE_OPTIONS seeding — Env is empty,
	// exactly like the live settings.json probed during investigation.
	if err := config.SaveSettings(&config.Settings{
		ACPServers: []config.ACPServerSettings{
			{Name: "Auggie (Opus)", Command: "auggie --acp"},
		},
	}); err != nil {
		t.Fatalf("SaveSettings (seed): %v", err)
	}

	// Deploy a minimal agent definition under MITTO_DIR/agents/builtin/augment
	// whose metadata.yaml declares defaults.env.NODE_OPTIONS, mirroring
	// config/agents/builtin/augment/metadata.yaml in the real tree.
	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		t.Fatalf("AgentsDir: %v", err)
	}
	augmentDir := filepath.Join(agentsDir, "builtin", "augment")
	if err := os.MkdirAll(augmentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	metaYAML := []byte(`name: augment
displayName: Augment
acpId: auggie
description: test
defaults:
  env:
    NODE_OPTIONS: "--max-old-space-size=12288"
`)
	if err := os.WriteFile(filepath.Join(augmentDir, "metadata.yaml"), metaYAML, 0644); err != nil {
		t.Fatalf("WriteFile metadata.yaml: %v", err)
	}

	h := New(Deps{})

	// Re-confirm the SAME agent (same Name as the pre-existing entry), as the
	// agent-discovery UI would do if the user re-runs "Scan for agents" and
	// re-confirms it (e.g. to pick up the new default after an update).
	reqBody, err := json.Marshal(AgentConfirmRequest{
		Agents: []AgentConfirmEntry{
			{Name: "Auggie (Opus)", Command: "auggie --acp", DirName: "augment"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/confirm", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.HandleConfirmAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	// Read back settings.json and inspect the (only) "Auggie (Opus)" entry's Env.
	var reloaded config.Settings
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile settings.json: %v", err)
	}
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("Unmarshal settings.json: %v", err)
	}

	var found *config.ACPServerSettings
	for i := range reloaded.ACPServers {
		if reloaded.ACPServers[i].Name == "Auggie (Opus)" {
			found = &reloaded.ACPServers[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("Auggie (Opus) server entry not found in settings.json after confirm; got %+v", reloaded.ACPServers)
	}

	// This is the bug: the existing entry's Env is never backfilled, so
	// NODE_OPTIONS is absent and the subprocess falls back to V8's own
	// heap default instead of the agent's intended 12288 MB cap.
	got, ok := found.Env["NODE_OPTIONS"]
	if !ok || got == "" {
		t.Fatalf("mitto-qphs reproduced: existing ACP server entry %q has no NODE_OPTIONS "+
			"after re-confirming an agent whose metadata declares defaults.env.NODE_OPTIONS=%q "+
			"(got Env=%v) — the agent's V8 heap-cap default never reaches the subprocess, "+
			"so it falls back to V8's own ~6 GiB default and can abort with SIGABRT under memory pressure",
			"Auggie (Opus)", "--max-old-space-size=12288", found.Env)
	}
}

// TestHandleConfirmAgents_NewGithubCopilotAgent_MissingACPFlag reproduces
// mitto-hc4: confirming a brand-new "GitHub Copilot" agent from the
// Discover-Agents dialog persists the BARE binary name ("copilot") as the
// runtime ACPServerSettings.Command, silently dropping the agent's
// metadata.yaml install.args ("--acp").
//
// Root cause (see the Investigation comment on mitto-hc4): status.sh emits
// the bare binary as status.command, AgentDiscoveryDialog.js's handleConfirm
// sends that verbatim as AgentConfirmEntry.Command, and HandleConfirmAgents
// (agent_discovery.go) persists entry.Command unchanged — seedACPServerDefaults
// only seeds Env/Tags/Constraints/AutoApprove/ContextFlushCommand, never
// Command. metadata.yaml's install.args is consumed ONLY by the npx install
// flow, never appended to the discovered runtime command.
//
// Without --acp, `copilot` launches its interactive TUI instead of its ACP
// stdio server, never answers ACP `initialize`, and Mitto's startup watchdog
// SIGKILLs it (failed_to_start / -32603).
//
// This test confirms the currently-missing behavior: confirming a new
// "GitHub Copilot" agent whose metadata declares install.args=["--acp"] should
// produce a persisted Command that includes "--acp" (mirroring the real
// config/agents/builtin/github-copilot/metadata.yaml). Today it does not —
// the persisted Command is the bare "copilot", exactly like status.sh emits.
func TestHandleConfirmAgents_NewGithubCopilotAgent_MissingACPFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// No pre-existing settings.json: this is a brand-new agent being added via
	// Discover Agents, the exact path mitto-hc4 was filed against.
	if err := config.SaveSettings(&config.Settings{}); err != nil {
		t.Fatalf("SaveSettings (seed): %v", err)
	}

	// Deploy a minimal agent definition under MITTO_DIR/agents/builtin/github-copilot
	// whose metadata.yaml declares install.args=["--acp"], mirroring
	// config/agents/builtin/github-copilot/metadata.yaml in the real tree.
	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		t.Fatalf("AgentsDir: %v", err)
	}
	copilotDir := filepath.Join(agentsDir, "builtin", "github-copilot")
	if err := os.MkdirAll(copilotDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	metaYAML := []byte(`name: "GitHub Copilot"
displayName: "GitHub Copilot"
acpId: "github-copilot"
description: test
install:
  method: "npx"
  package: "@github/copilot"
  args: ["--acp"]
`)
	if err := os.WriteFile(filepath.Join(copilotDir, "metadata.yaml"), metaYAML, 0644); err != nil {
		t.Fatalf("WriteFile metadata.yaml: %v", err)
	}

	h := New(Deps{})

	// Confirm the agent exactly as AgentDiscoveryDialog.js's handleConfirm
	// would: command is the BARE binary from status.sh's status.command,
	// with no --acp appended.
	reqBody, err := json.Marshal(AgentConfirmRequest{
		Agents: []AgentConfirmEntry{
			{Name: "GitHub Copilot", Command: "copilot", DirName: "github-copilot"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/confirm", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.HandleConfirmAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	// Read back settings.json and inspect the persisted "GitHub Copilot" entry's Command.
	var reloaded config.Settings
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile settings.json: %v", err)
	}
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("Unmarshal settings.json: %v", err)
	}

	var found *config.ACPServerSettings
	for i := range reloaded.ACPServers {
		if reloaded.ACPServers[i].Name == "GitHub Copilot" {
			found = &reloaded.ACPServers[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("GitHub Copilot server entry not found in settings.json after confirm; got %+v", reloaded.ACPServers)
	}

	// This is the bug: the persisted Command is the bare "copilot", missing
	// the mandatory --acp flag declared in the agent's metadata install.args,
	// so a new GitHub Copilot ACP session will never start (mitto-hc4).
	if !strings.Contains(found.Command, "--acp") {
		t.Fatalf("mitto-hc4 reproduced: persisted ACP server entry %q has Command=%q, missing "+
			"the mandatory --acp flag declared in the agent's metadata.yaml install.args=[\"--acp\"] "+
			"— copilot will launch its interactive TUI instead of its ACP stdio server, never answer "+
			"initialize, and Mitto's startup watchdog will SIGKILL it (failed_to_start / -32603)",
			"GitHub Copilot", found.Command)
	}
}
