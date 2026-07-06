package agents

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBuiltinAgents_StderrPatternsCompile is the validator behind
// `make check-stderr-patterns` (mitto-k6h). It walks config/agents/builtin/*
// on disk, parses each metadata.yaml, and asserts that every regex in
// stderrPatterns.crash / .ignore / .degraded compiles cleanly with
// regexp.Compile. A single bad regex fails the test — this catches typos in
// YAML at CI time before they show up as skip-with-warn at runtime.
func TestBuiltinAgents_StderrPatternsCompile(t *testing.T) {
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

		if meta.StderrPatterns == nil {
			continue
		}

		checkClass(t, entry.Name(), "crash", meta.StderrPatterns.Crash)
		checkClass(t, entry.Name(), "ignore", meta.StderrPatterns.Ignore)
		checkClass(t, entry.Name(), "degraded", meta.StderrPatterns.Degraded)
		checked++
	}

	if checked == 0 {
		t.Fatalf("no builtin agent metadata.yaml with stderrPatterns found under %s", builtinDir)
	}
}

// checkClass fails the test if any pattern in the list is not a valid regex.
func checkClass(t *testing.T, agentDir, class string, patterns []string) {
	t.Helper()
	for i, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			t.Errorf("%s: stderrPatterns.%s[%d] = %q does not compile: %v",
				agentDir, class, i, p, err)
		}
	}
}

// builtinAgentsDirForTest returns the absolute path to config/agents/builtin
// relative to the test's source file location. This works regardless of the
// current working directory (the test can be run via `go test ./internal/agents/...`
// or from the package directory).
func builtinAgentsDirForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/agents/stderr_patterns_test.go
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(repoRoot, "config", "agents", "builtin")
}
