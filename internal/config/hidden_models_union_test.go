package config

import (
	"errors"
	"strings"
	"testing"
)

func TestCombineAgentWorkspacePolicies(t *testing.T) {
	t.Run("nil agent and nil rules yields nil", func(t *testing.T) {
		policy, err := CombineAgentWorkspacePolicies("srv", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if policy != nil {
			t.Fatalf("expected nil policy, got %+v", policy)
		}
	})

	t.Run("empty workspace rules keep agent policy", func(t *testing.T) {
		agent, err := CompileHiddenModels("srv", []string{"agent-.*"})
		if err != nil {
			t.Fatalf("compile agent: %v", err)
		}
		policy, err := CombineAgentWorkspacePolicies("srv", agent, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !policy.Hides("agent-1", "") {
			t.Fatal("expected agent pattern to still hide")
		}
		if policy.Hides("model-1", "") {
			t.Fatal("unexpected hide")
		}
	})

	t.Run("workspace-only rules apply", func(t *testing.T) {
		policy, err := CombineAgentWorkspacePolicies("srv", nil, []string{"^ws-"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if policy == nil {
			t.Fatal("expected non-nil workspace policy")
		}
		if !policy.Hides("ws-1", "") || policy.Hides("other", "") {
			t.Fatalf("workspace-only union wrong: %v", policy.Hides("ws-1", ""))
		}
	})

	t.Run("union hides what either level hides", func(t *testing.T) {
		agent, err := CompileHiddenModels("srv", []string{"^a-secret"})
		if err != nil {
			t.Fatalf("compile agent: %v", err)
		}
		policy, err := CombineAgentWorkspacePolicies("srv", agent, []string{"^w-secret"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !policy.Hides("a-secret-model", "") {
			t.Fatal("agent half lost")
		}
		if !policy.Hides("w-secret-model", "") {
			t.Fatal("workspace half lost")
		}
		if policy.Hides("open-model", "") {
			t.Fatal("unexpected hide")
		}
		// Case-insensitive semantics preserved.
		if !policy.Hides("A-SECRET-model", "") {
			t.Fatal("case-insensitive match lost")
		}
	})

	t.Run("invalid workspace rule fails closed with HiddenModelValidationError", func(t *testing.T) {
		policy, err := CombineAgentWorkspacePolicies("srv", nil, []string{"("})
		if err == nil {
			t.Fatal("expected error")
		}
		var verr *HiddenModelValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("expected *HiddenModelValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(verr.Field, "hidden_models[") {
			t.Fatalf("unexpected field label: %q", verr.Field)
		}
		if !strings.Contains(err.Error(), "workspace") {
			t.Fatalf("expected server label in error: %v", err)
		}
		if policy == nil {
			t.Fatal("expected non-nil AllMatches policy")
		}
		if !policy.Hides("anything", "") || !policy.Hides("", "anything") {
			t.Fatal("expected fail-closed AllMatches policy")
		}
	})

	t.Run("invalid workspace rule hides model names too", func(t *testing.T) {
		agent, err := CompileHiddenModels("srv", []string{"^a-secret"})
		if err != nil {
			t.Fatalf("compile agent: %v", err)
		}
		policy, _ := CombineAgentWorkspacePolicies("srv", agent, []string{"("})
		if agent == nil || policy == nil {
			t.Fatal("policies must be non-nil")
		}
		if !policy.Hides("w-anything", "") {
			t.Fatal("fail closed must hide workspace-only IDs")
		}
		if !policy.Hides("a-anything", "") {
			t.Fatal("fail closed must hide agent-only IDs")
		}
	})
}

func TestAllMatchesPolicyHidesEverything(t *testing.T) {
	p := AllMatchesPolicy("srv")
	for _, m := range []struct{ id, name string }{
		{"some-model", "Some Model"},
		{"a嘗試", "嘗試"},
		{"", "name-only"},
		{"", ""},
	} {
		if !p.Hides(m.id, m.name) {
			t.Fatalf("expected AllMatches to hide %q/%q", m.id, m.name)
		}
	}
}
