package procstart

import (
	"strings"
	"testing"
)

// TestBuildACPProcessEnv verifies env-layering for ACP subprocess startup.
// Layering: os.Environ() < server-specific Env < MITTO_* vars.
func TestBuildACPProcessEnv(t *testing.T) {
	t.Setenv("MITTO_TEST_BASE_ENV", "from-base")

	t.Run("includes os.Environ", func(t *testing.T) {
		env := BuildACPProcessEnv(nil, nil)
		if !envContainsKV(env, "MITTO_TEST_BASE_ENV", "from-base") {
			t.Errorf("expected MITTO_TEST_BASE_ENV=from-base in env, got %v entries", len(env))
		}
	})

	t.Run("appends server-specific env", func(t *testing.T) {
		env := BuildACPProcessEnv(map[string]string{"FOO": "bar", "BAZ": "qux"}, nil)
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
	result := BuildACPProcessEnv(serverEnv, nil)

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
	result := BuildACPProcessEnv(serverEnv, mittoEnv)

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
