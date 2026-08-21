package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBuiltinAgents_NodeAgentsRaiseV8HeapLimit is the regression guard for
// mitto-54k.10 (Auggie/Node subprocess V8 out-of-memory crash). Node-based
// ACP agents (installed via npx) default to V8's built-in old-space heap cap
// (~4-6 GB); large agent turns on big workspaces can exhaust it and abort the
// process with FATAL ERROR: Ineffective mark-compacts near heap limit,
// tearing down every session sharing the process. Mitto owns the subprocess
// env at launch so it must raise the cap by setting NODE_OPTIONS on the
// agent's Defaults.Env, which reaches the subprocess two ways: seeded into
// ACPServerSettings.Env at discovery, and — since mitto-6dur — resolved live
// by the AgentDefaultEnvResolver wired in internal/web/server.go into
// procstart.BuildACPProcessEnv's agent-defaults layer, so installs whose
// settings.json acp_servers[].env predates discovery-time seeding still get
// the default at spawn time (an explicit acp_servers[].env entry still
// overrides it).
//
// This test walks config/agents/builtin/* and asserts that every agent whose
// Install.Method == "npx" declares a NODE_OPTIONS entry in Defaults.Env with
// an explicit --max-old-space-size= override.
func TestBuiltinAgents_NodeAgentsRaiseV8HeapLimit(t *testing.T) {
	builtinDir := builtinAgentsDirForTest(t)

	entries, err := os.ReadDir(builtinDir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", builtinDir, err)
	}

	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(builtinDir, entry.Name(), "metadata.yaml")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("cannot read %s: %v", metaPath, err)
		}

		var meta AgentMetadata
		if err := yaml.Unmarshal(data, &meta); err != nil {
			t.Fatalf("cannot parse %s: %v", metaPath, err)
		}

		if meta.Install == nil || meta.Install.Method != "npx" {
			continue
		}
		checked++

		if meta.Defaults == nil {
			t.Errorf("%s: Node-based agent (install.method=npx) has no defaults block; expected defaults.env.NODE_OPTIONS with --max-old-space-size=... (mitto-54k.10)", entry.Name())
			continue
		}
		got, ok := meta.Defaults.Env["NODE_OPTIONS"]
		if !ok {
			t.Errorf("%s: Node-based agent has no defaults.env.NODE_OPTIONS entry; expected --max-old-space-size=... override (mitto-54k.10)", entry.Name())
			continue
		}
		if !strings.Contains(got, "--max-old-space-size=") {
			t.Errorf("%s: defaults.env.NODE_OPTIONS = %q does not raise V8 old-space limit; expected substring --max-old-space-size=<MB> (mitto-54k.10)", entry.Name(), got)
		}
	}

	if checked == 0 {
		t.Fatalf("no Node-based (install.method=npx) builtin agents found under %s", builtinDir)
	}
}
