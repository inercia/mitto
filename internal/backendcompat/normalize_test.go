package backendcompat

import (
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestResolveProviderAlias_ExactMatch(t *testing.T) {
	got, err := ResolveProviderAlias("Auggie", []string{"Auggie", "Claude Code"})
	if err != nil {
		t.Fatalf("ResolveProviderAlias() error = %v", err)
	}
	if got != "Auggie" {
		t.Fatalf("got %q, want %q", got, "Auggie")
	}
}

func TestResolveProviderAlias_CaseInsensitiveMatch(t *testing.T) {
	// Mirrors session/migration_001_normalize_acp.go's "auggie" -> "Auggie" semantics.
	got, err := ResolveProviderAlias("auggie", []string{"Auggie", "Claude Code"})
	if err != nil {
		t.Fatalf("ResolveProviderAlias() error = %v", err)
	}
	if got != "Auggie" {
		t.Fatalf("got %q, want canonical %q", got, "Auggie")
	}
}

func TestResolveProviderAlias_NoMatchLeftUnchanged(t *testing.T) {
	// Provider deleted/renamed away: leave the stale alias unchanged rather
	// than guessing or erroring, matching migration_001's "not found" path.
	got, err := ResolveProviderAlias("deleted-server", []string{"Auggie"})
	if err != nil {
		t.Fatalf("ResolveProviderAlias() error = %v", err)
	}
	if got != "deleted-server" {
		t.Fatalf("got %q, want unchanged alias %q", got, "deleted-server")
	}
}

func TestResolveProviderAlias_AmbiguousCanonicalSetRejected(t *testing.T) {
	// Two canonical names fold to the same lowercase form: must reject
	// rather than silently pick the first one (unlike migration_001).
	_, err := ResolveProviderAlias("auggie", []string{"Auggie", "AUGGIE"})
	if err == nil {
		t.Fatal("ResolveProviderAlias() error = nil, want ErrAmbiguousAlias for a duplicate-folding canonical set")
	}
	if !errors.Is(err, agentbackend.ErrAmbiguousAlias) {
		t.Fatalf("errors.Is(err, ErrAmbiguousAlias) = false for err = %v", err)
	}
}

func TestResolveProviderAlias_EmptyAliasRejected(t *testing.T) {
	if _, err := ResolveProviderAlias("", []string{"Auggie"}); err == nil {
		t.Fatal("ResolveProviderAlias(\"\", ...) error = nil, want error")
	}
}

func TestReconcileProviderID_BothEmpty(t *testing.T) {
	got, err := ReconcileProviderID("", "")
	if err != nil || got != "" {
		t.Fatalf("ReconcileProviderID(\"\", \"\") = (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestReconcileProviderID_OnlyLegacySet(t *testing.T) {
	got, err := ReconcileProviderID("Auggie", "")
	if err != nil {
		t.Fatalf("ReconcileProviderID() error = %v", err)
	}
	if got != "Auggie" {
		t.Fatalf("got %q, want %q (legacy wins when neutral is unset)", got, "Auggie")
	}
}

func TestReconcileProviderID_OnlyNeutralSet(t *testing.T) {
	got, err := ReconcileProviderID("", agentbackend.ProviderID("Auggie"))
	if err != nil {
		t.Fatalf("ReconcileProviderID() error = %v", err)
	}
	if got != "Auggie" {
		t.Fatalf("got %q, want %q (neutral wins when legacy is unset)", got, "Auggie")
	}
}

func TestReconcileProviderID_AgreeingValuesOK(t *testing.T) {
	got, err := ReconcileProviderID("Auggie", agentbackend.ProviderID("Auggie"))
	if err != nil {
		t.Fatalf("ReconcileProviderID() error = %v", err)
	}
	if got != "Auggie" {
		t.Fatalf("got %q, want %q", got, "Auggie")
	}
}

func TestReconcileProviderID_ConflictRejected(t *testing.T) {
	_, err := ReconcileProviderID("Auggie", agentbackend.ProviderID("ClaudeCode"))
	if err == nil {
		t.Fatal("ReconcileProviderID() error = nil, want ErrConflictingIdentity when legacy and neutral disagree")
	}
	if !errors.Is(err, agentbackend.ErrConflictingIdentity) {
		t.Fatalf("errors.Is(err, ErrConflictingIdentity) = false for err = %v", err)
	}
}
