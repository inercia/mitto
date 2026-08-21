package procstart

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBuildACPProcessEnv_AgentMetadataDefaultsReachSubprocess is the
// reproduction/regression test for mitto-6dur: the agent-metadata
// `defaults.env.NODE_OPTIONS` declared in
// config/agents/builtin/augment/metadata.yaml must reach a spawned ACP
// subprocess via BuildACPProcessEnv's agent-defaults layer. Before the fix,
// BuildACPProcessEnv had no parameter at all to carry
// agents.AgentMetadata.Defaults.Env, so on an installation whose ACP server
// entry still has an empty Env (servers confirmed before mitto-qphs, or
// added without dir_name — the reported production host state), the
// subprocess got NO NODE_OPTIONS at all and Node fell back to V8's ~4GB
// default heap, which OOMs on large agent turns (production incident: V8
// fatal at 4192.4MB while metadata declares --max-old-space-size=12288).
//
// This test parses the real augment/metadata.yaml directly (no
// internal/agents import, to avoid introducing a new package coupling from
// this leaf package — mirrors the yaml-parsing approach already used by
// internal/agents/builtin_node_heap_test.go) to obtain the expected
// NODE_OPTIONS default, then calls BuildACPProcessEnv exactly as production
// does today when acp_servers[].env is empty and the agent-defaults
// resolver (mitto-k6h-style, wired in internal/web/server.go) has resolved
// the metadata default, and asserts it is present in the resulting
// subprocess env. It also pins the documented override rule: an explicit
// acp_servers[].env entry in settings.json must still win over the
// agent-authored default (so a user can lower the cap).
func TestBuildACPProcessEnv_AgentMetadataDefaultsReachSubprocess(t *testing.T) {
	metaPath := augmentMetadataPathForTest(t)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", metaPath, err)
	}

	var meta struct {
		Defaults struct {
			Env map[string]string `yaml:"env"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		t.Fatalf("cannot parse %s: %v", metaPath, err)
	}

	const wantKey = "NODE_OPTIONS"
	wantVal, ok := meta.Defaults.Env[wantKey]
	if !ok || !strings.Contains(wantVal, "--max-old-space-size=") {
		t.Fatalf("precondition failed: %s defaults.env.NODE_OPTIONS = %q, want a --max-old-space-size= override", metaPath, wantVal)
	}

	// Make the assertion deterministic regardless of the ambient test
	// environment: ensure NODE_OPTIONS is not already set from outside.
	orig, hadOrig := os.LookupEnv(wantKey)
	if err := os.Unsetenv(wantKey); err != nil {
		t.Fatalf("os.Unsetenv(%s): %v", wantKey, err)
	}
	if hadOrig {
		t.Cleanup(func() { _ = os.Setenv(wantKey, orig) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(wantKey) })
	}

	t.Run("empty serverEnv still gets the agent-metadata default", func(t *testing.T) {
		// Simulate the reported host state: settings.json acp_servers[].env is
		// still empty (server confirmed before mitto-qphs, or without
		// dir_name), so serverEnv carries nothing. mittoEnv/agentHintEnv are
		// irrelevant to this bug and left nil — this mirrors exactly what
		// shared_acp_process.go and bgsession_acp_process.go pass today, with
		// the resolved agent-metadata defaults as the new 4th argument.
		env := BuildACPProcessEnv(nil, nil, nil, meta.Defaults.Env)

		got, found := lastValueFor(env, wantKey)
		if !found {
			t.Fatalf("BuildACPProcessEnv did not propagate agent-metadata default %s=%q into the subprocess env (mitto-6dur): BuildACPProcessEnv has no agent-defaults layer", wantKey, wantVal)
		}
		if got != wantVal {
			t.Errorf("%s = %q, want %q (agent-metadata default)", wantKey, got, wantVal)
		}
	})

	t.Run("explicit serverEnv still overrides the agent-metadata default", func(t *testing.T) {
		// A user who has explicitly configured acp_servers[].env.NODE_OPTIONS
		// (e.g. to lower the cap) must not have it silently clobbered by the
		// agent-metadata default.
		serverEnv := map[string]string{wantKey: "--max-old-space-size=2048"}
		env := BuildACPProcessEnv(serverEnv, nil, nil, meta.Defaults.Env)

		got, found := lastValueFor(env, wantKey)
		if !found {
			t.Fatalf("expected %s to be present in env", wantKey)
		}
		if got != "--max-old-space-size=2048" {
			t.Errorf("%s = %q, want %q (explicit serverEnv must win over agent-metadata default)", wantKey, got, "--max-old-space-size=2048")
		}
	})
}

// augmentMetadataPathForTest resolves the real
// config/agents/builtin/augment/metadata.yaml path relative to this test
// file's location, mirroring internal/agents' builtinAgentsDirForTest
// helper (internal/agents/stderr_patterns_test.go).
func augmentMetadataPathForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/acpproc/procstart/env_agent_defaults_test.go
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	return filepath.Join(repoRoot, "config", "agents", "builtin", "augment", "metadata.yaml")
}
