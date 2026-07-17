package procstart

import (
	"strings"
	"testing"
)

// TestBuildACPProcessEnv verifies env-layering for ACP subprocess startup.
// Layering: os.Environ() < agentHintEnv < server-specific Env < MITTO_* vars.
func TestBuildACPProcessEnv(t *testing.T) {
	t.Setenv("MITTO_TEST_BASE_ENV", "from-base")

	t.Run("includes os.Environ", func(t *testing.T) {
		env := BuildACPProcessEnv(nil, nil, nil)
		if !envContainsKV(env, "MITTO_TEST_BASE_ENV", "from-base") {
			t.Errorf("expected MITTO_TEST_BASE_ENV=from-base in env, got %v entries", len(env))
		}
	})

	t.Run("appends server-specific env", func(t *testing.T) {
		env := BuildACPProcessEnv(map[string]string{"FOO": "bar", "BAZ": "qux"}, nil, nil)
		if !envContainsKV(env, "FOO", "bar") {
			t.Error("expected FOO=bar in env")
		}
		if !envContainsKV(env, "BAZ", "qux") {
			t.Error("expected BAZ=qux in env")
		}
	})

	t.Run("appends mitto env after server env (mitto wins)", func(t *testing.T) {
		// Same key in both — later append wins by os.Exec semantics.
		env := BuildACPProcessEnv(
			map[string]string{"OVERLAP": "from-server"},
			map[string]string{"OVERLAP": "from-mitto", "MITTO_SESSION_ID": "abc"},
			nil,
		)
		// Find the LAST occurrence of OVERLAP=...
		lastOverlap := ""
		for _, kv := range env {
			if strings.HasPrefix(kv, "OVERLAP=") {
				lastOverlap = kv
			}
		}
		if lastOverlap != "OVERLAP=from-mitto" {
			t.Errorf("expected final OVERLAP=from-mitto (mitto wins), got %q", lastOverlap)
		}
		if !envContainsKV(env, "MITTO_SESSION_ID", "abc") {
			t.Error("expected MITTO_SESSION_ID=abc in env")
		}
	})
}

func envContainsKV(env []string, key, value string) bool {
	want := key + "=" + value
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestBuildACPProcessEnv_ReplacesExistingKey(t *testing.T) {
	// Ensure NODE_OPTIONS exists in os.Environ() with a different value.
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=2048")

	serverEnv := map[string]string{
		"NODE_OPTIONS": "--max-old-space-size=6144",
	}
	result := BuildACPProcessEnv(serverEnv, nil, nil)

	var found []string
	for _, kv := range result {
		if strings.HasPrefix(kv, "NODE_OPTIONS=") {
			found = append(found, kv)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one NODE_OPTIONS entry, got %d: %v", len(found), found)
	}
	if found[0] != "NODE_OPTIONS=--max-old-space-size=6144" {
		t.Errorf("expected NODE_OPTIONS=--max-old-space-size=6144, got %s", found[0])
	}
}

func TestBuildACPProcessEnv_MittoEnvOverridesServerEnv(t *testing.T) {
	serverEnv := map[string]string{
		"MITTO_TEST_VAR": "from-server",
	}
	mittoEnv := map[string]string{
		"MITTO_TEST_VAR": "from-mitto",
	}
	result := BuildACPProcessEnv(serverEnv, mittoEnv, nil)

	var found []string
	for _, kv := range result {
		if strings.HasPrefix(kv, "MITTO_TEST_VAR=") {
			found = append(found, kv)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one MITTO_TEST_VAR entry, got %d: %v", len(found), found)
	}
	if found[0] != "MITTO_TEST_VAR=from-mitto" {
		t.Errorf("expected MITTO_TEST_VAR=from-mitto, got %s", found[0])
	}
}

// lastValueFor returns the value of the last occurrence of key in an env
// slice, or ("", false) if the key is not present. Matches exec semantics
// where a duplicated key resolves to the last-appearing entry.
func lastValueFor(env []string, key string) (string, bool) {
	prefix := key + "="
	found := false
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			last = kv[len(prefix):]
			found = true
		}
	}
	return last, found
}

// TestBuildACPProcessEnv_AgentHintLayer covers mitto-g7ye:
//   - AC #2: an ACP subprocess spawned via this env builder (used by both the
//     direct-exec and restricted-runner branches in shared_acp_process.go and
//     bgsession_acp_process.go) sees AGENT_MODE=1 in its env.
//   - AC #3: settings.json acp_servers[].env (modelled here as serverEnv) can
//     override AGENT_MODE — either to a custom value or to empty (disable) —
//     because the agent-hint layer sits below serverEnv.
//
// The two ACP-start call sites both delegate to BuildACPProcessEnv, so
// exercising the builder covers both branches (verified by inspection of
// internal/acpproc/shared_acp_process.go and
// internal/conversation/bgsession_acp_process.go).
func TestBuildACPProcessEnv_AgentHintLayer(t *testing.T) {
	agentHint := map[string]string{"AGENT_MODE": "1"}

	t.Run("agent hint reaches subprocess env by default", func(t *testing.T) {
		env := BuildACPProcessEnv(nil, nil, agentHint)
		got, ok := lastValueFor(env, "AGENT_MODE")
		if !ok {
			t.Fatalf("expected AGENT_MODE to be present in env")
		}
		if got != "1" {
			t.Errorf("AGENT_MODE = %q, want %q", got, "1")
		}
	})

	t.Run("serverEnv overrides agent hint with a custom value", func(t *testing.T) {
		serverEnv := map[string]string{"AGENT_MODE": "verbose"}
		env := BuildACPProcessEnv(serverEnv, nil, agentHint)
		got, ok := lastValueFor(env, "AGENT_MODE")
		if !ok {
			t.Fatalf("expected AGENT_MODE to be present in env")
		}
		if got != "verbose" {
			t.Errorf("AGENT_MODE = %q, want %q (serverEnv must win over agent hint)", got, "verbose")
		}
	})

	t.Run("serverEnv can disable agent hint by setting it to empty", func(t *testing.T) {
		// AC #3 explicitly calls out: "user can set AGENT_MODE=empty in
		// settings.json to disable it". The final value must be the empty
		// serverEnv override, not the hint's "1".
		serverEnv := map[string]string{"AGENT_MODE": ""}
		env := BuildACPProcessEnv(serverEnv, nil, agentHint)
		got, ok := lastValueFor(env, "AGENT_MODE")
		if !ok {
			t.Fatalf("expected AGENT_MODE to be present in env (with empty value), got absent")
		}
		if got != "" {
			t.Errorf("AGENT_MODE = %q, want empty (serverEnv override disables hint)", got)
		}
	})

	t.Run("mitto identity vars still outrank agent hint on collision", func(t *testing.T) {
		// Belt-and-braces: agent-hint layer must never displace MITTO_* identity
		// vars. If both maps set the same key, MITTO wins (highest precedence).
		hint := map[string]string{"MITTO_COLLISION": "from-hint"}
		mittoEnv := map[string]string{"MITTO_COLLISION": "from-mitto"}
		env := BuildACPProcessEnv(nil, mittoEnv, hint)
		got, ok := lastValueFor(env, "MITTO_COLLISION")
		if !ok {
			t.Fatalf("expected MITTO_COLLISION to be present")
		}
		if got != "from-mitto" {
			t.Errorf("MITTO_COLLISION = %q, want %q (mitto identity must win)", got, "from-mitto")
		}
	})
}
