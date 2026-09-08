package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
)

func testAgentSelectionConfig() *config.Config {
	return &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "Auggie", Command: "auggie --acp"},
			{Name: "Claude Code", Command: "claude-code --acp"},
		},
	}
}

func TestResolveAgentSelector(t *testing.T) {
	cfg := testAgentSelectionConfig()

	t.Run("nil config errors", func(t *testing.T) {
		if _, err := resolveAgentSelector(nil, "Auggie"); err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("empty selector errors", func(t *testing.T) {
		if _, err := resolveAgentSelector(cfg, ""); err == nil {
			t.Fatal("expected error for empty selector")
		}
	})

	t.Run("exact match", func(t *testing.T) {
		srv, err := resolveAgentSelector(cfg, "Auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Auggie" {
			t.Fatalf("got %q, want %q", srv.Name, "Auggie")
		}
	})

	t.Run("case-insensitive alias", func(t *testing.T) {
		srv, err := resolveAgentSelector(cfg, "auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Auggie" {
			t.Fatalf("got %q, want canonical %q", srv.Name, "Auggie")
		}
	})

	t.Run("unknown selector errors", func(t *testing.T) {
		if _, err := resolveAgentSelector(cfg, "does-not-exist"); err == nil {
			t.Fatal("expected error for unknown agent selector")
		}
	})

	t.Run("ambiguous canonical set rejected", func(t *testing.T) {
		ambiguous := &config.Config{ACPServers: []config.ACPServer{
			{Name: "Auggie"}, {Name: "AUGGIE"},
		}}
		_, err := resolveAgentSelector(ambiguous, "auggie")
		if err == nil {
			t.Fatal("expected ErrAmbiguousAlias for a duplicate-folding canonical set")
		}
		if !errors.Is(err, agentbackend.ErrAmbiguousAlias) {
			t.Fatalf("errors.Is(err, ErrAmbiguousAlias) = false for err = %v", err)
		}
	})
}

func TestResolveServerSelector(t *testing.T) {
	cfg := testAgentSelectionConfig()

	t.Run("nil config errors", func(t *testing.T) {
		if _, err := resolveServerSelector(nil, "Auggie", ""); err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("neither set returns nil,nil for caller default", func(t *testing.T) {
		srv, err := resolveServerSelector(cfg, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv != nil {
			t.Fatalf("got %v, want nil so caller falls back to its own default", srv)
		}
	})

	t.Run("only acp set wins", func(t *testing.T) {
		srv, err := resolveServerSelector(cfg, "Claude Code", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Claude Code" {
			t.Fatalf("got %q, want %q", srv.Name, "Claude Code")
		}
	})

	t.Run("only agent set wins, exact", func(t *testing.T) {
		srv, err := resolveServerSelector(cfg, "", "Auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Auggie" {
			t.Fatalf("got %q, want %q", srv.Name, "Auggie")
		}
	})

	t.Run("only agent set wins, case-insensitive alias", func(t *testing.T) {
		srv, err := resolveServerSelector(cfg, "", "claude code")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Claude Code" {
			t.Fatalf("got %q, want canonical %q", srv.Name, "Claude Code")
		}
	})

	t.Run("both set and agree", func(t *testing.T) {
		srv, err := resolveServerSelector(cfg, "Auggie", "auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if srv.Name != "Auggie" {
			t.Fatalf("got %q, want %q", srv.Name, "Auggie")
		}
	})

	t.Run("both set and disagree errors with actionable message", func(t *testing.T) {
		_, err := resolveServerSelector(cfg, "Auggie", "Claude Code")
		if err == nil {
			t.Fatal("expected conflict error when --acp and --agent resolve to different servers")
		}
		if !strings.Contains(err.Error(), "Auggie") || !strings.Contains(err.Error(), "Claude Code") {
			t.Fatalf("error %q does not mention both conflicting values", err.Error())
		}
	})

	t.Run("unknown acp value errors", func(t *testing.T) {
		if _, err := resolveServerSelector(cfg, "does-not-exist", ""); err == nil {
			t.Fatal("expected error for unknown --acp value")
		}
	})

	t.Run("unknown agent value errors", func(t *testing.T) {
		if _, err := resolveServerSelector(cfg, "", "does-not-exist"); err == nil {
			t.Fatal("expected error for unknown --agent value")
		}
	})
}

func TestResolveConversationNewACPServer(t *testing.T) {
	t.Run("neither set", func(t *testing.T) {
		got, err := resolveConversationNewACPServer("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("only acp set wins", func(t *testing.T) {
		got, err := resolveConversationNewACPServer("Auggie", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Auggie" {
			t.Fatalf("got %q, want %q", got, "Auggie")
		}
	})

	t.Run("only agent set wins", func(t *testing.T) {
		got, err := resolveConversationNewACPServer("", "Auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Auggie" {
			t.Fatalf("got %q, want %q", got, "Auggie")
		}
	})

	t.Run("both set and identical", func(t *testing.T) {
		got, err := resolveConversationNewACPServer("Auggie", "Auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Auggie" {
			t.Fatalf("got %q, want %q", got, "Auggie")
		}
	})

	t.Run("both set and disagree errors with actionable message", func(t *testing.T) {
		_, err := resolveConversationNewACPServer("Auggie", "Claude Code")
		if err == nil {
			t.Fatal("expected conflict error when --acp and --agent disagree")
		}
		if !strings.Contains(err.Error(), "Auggie") || !strings.Contains(err.Error(), "Claude Code") {
			t.Fatalf("error %q does not mention both conflicting values", err.Error())
		}
	})

	t.Run("both set and disagree only by case is still a conflict (literal comparison, no local config)", func(t *testing.T) {
		// conversation_new talks to the REST API without local server config,
		// so this path cannot canonicalize aliases like resolveServerSelector
		// does; a case-only difference is treated as a real conflict.
		_, err := resolveConversationNewACPServer("Auggie", "auggie")
		if err == nil {
			t.Fatal("expected conflict error for a case-only mismatch (literal comparison)")
		}
	})
}
