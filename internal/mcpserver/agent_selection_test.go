package mcpserver

import (
	"errors"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
)

func testAgentSelectionMCPConfig() *config.Config {
	return &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "Auggie", Command: "auggie --acp"},
			{Name: "Claude Code", Command: "claude-code --acp"},
		},
	}
}

func TestResolveAgentOrACPServerName(t *testing.T) {
	cfg := testAgentSelectionMCPConfig()

	t.Run("nil config errors", func(t *testing.T) {
		if _, err := resolveAgentOrACPServerName(nil, "Auggie", ""); err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("both empty errors", func(t *testing.T) {
		if _, err := resolveAgentOrACPServerName(cfg, "", ""); err == nil {
			t.Fatal("expected error when both acp_server and agent are empty")
		}
	})

	t.Run("only acp_server set wins, exact match required", func(t *testing.T) {
		got, err := resolveAgentOrACPServerName(cfg, "Claude Code", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Claude Code" {
			t.Fatalf("got %q, want %q", got, "Claude Code")
		}
	})

	t.Run("acp_server is passed through verbatim even if unknown (exact-match-only contract)", func(t *testing.T) {
		// resolveAgentOrACPServerName does not itself validate acp_server
		// against the config — that's the caller's cfg.GetServer check.
		got, err := resolveAgentOrACPServerName(cfg, "unknown-server", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "unknown-server" {
			t.Fatalf("got %q, want passthrough %q", got, "unknown-server")
		}
	})

	t.Run("only agent set, exact match", func(t *testing.T) {
		got, err := resolveAgentOrACPServerName(cfg, "", "Auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Auggie" {
			t.Fatalf("got %q, want %q", got, "Auggie")
		}
	})

	t.Run("only agent set, case-insensitive alias", func(t *testing.T) {
		got, err := resolveAgentOrACPServerName(cfg, "", "claude code")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Claude Code" {
			t.Fatalf("got %q, want canonical %q", got, "Claude Code")
		}
	})

	t.Run("both set and agree (agent case-insensitive)", func(t *testing.T) {
		got, err := resolveAgentOrACPServerName(cfg, "Auggie", "auggie")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Auggie" {
			t.Fatalf("got %q, want %q", got, "Auggie")
		}
	})

	t.Run("both set and disagree errors with actionable message", func(t *testing.T) {
		_, err := resolveAgentOrACPServerName(cfg, "Auggie", "Claude Code")
		if err == nil {
			t.Fatal("expected conflict error when acp_server and agent resolve to different servers")
		}
		if !strings.Contains(err.Error(), "Auggie") || !strings.Contains(err.Error(), "Claude Code") {
			t.Fatalf("error %q does not mention both conflicting values", err.Error())
		}
	})

	t.Run("ambiguous canonical set rejected", func(t *testing.T) {
		ambiguous := &config.Config{ACPServers: []config.ACPServer{
			{Name: "Auggie"}, {Name: "AUGGIE"},
		}}
		_, err := resolveAgentOrACPServerName(ambiguous, "", "auggie")
		if err == nil {
			t.Fatal("expected ErrAmbiguousAlias for a duplicate-folding canonical set")
		}
		if !errors.Is(err, agentbackend.ErrAmbiguousAlias) {
			t.Fatalf("errors.Is(err, ErrAmbiguousAlias) = false for err = %v", err)
		}
	})
}
