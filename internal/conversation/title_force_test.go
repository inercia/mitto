package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// captureAuxProvider is a minimal auxiliary.ProcessProvider that records the
// composed prompt/purpose it receives and returns a canned response or error.
// Used by the mitto-yv2 Force-path tests below.
type captureAuxProvider struct {
	lastPrompt  string
	lastPurpose string
	response    string
	err         error
}

func (p *captureAuxProvider) PromptAuxiliary(_ context.Context, _, purpose, message string) (string, error) {
	p.lastPurpose = purpose
	p.lastPrompt = message
	if p.err != nil {
		return "", p.err
	}
	return p.response, nil
}

func (p *captureAuxProvider) PromptAuxiliaryAsync(_ context.Context, _, _, _ string) error {
	return nil
}

func (p *captureAuxProvider) CloseWorkspaceAuxiliary(_ string) error {
	return nil
}

// TestGenerateAndSetTitle_Force_SkipsQuickFallback verifies that Force:true
// bypasses the synchronous quick-fallback title write entirely (mitto-yv2):
// a forced regenerate is explicitly requested on a conversation that already
// has a title, so a low-quality fallback derived from the context excerpt
// would be pointless. The fallback write (when it happens) is synchronous,
// so checking metadata immediately after the call returns is deterministic.
func TestGenerateAndSetTitle_Force_SkipsQuickFallback(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "force-skip-fallback"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	provider := &captureAuxProvider{err: errors.New("aux not reached synchronously")}
	mgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)

	GenerateAndSetTitle(TitleGenerationConfig{
		Store:            store,
		SessionID:        sessionID,
		Message:          "User: fix the login bug\nAssistant: sure, looking into it",
		WorkspaceUUID:    "test-workspace",
		AuxiliaryManager: mgr,
		Force:            true,
	})

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Name != "" {
		t.Errorf("Force should skip the synchronous quick-fallback write, but Name = %q", meta.Name)
	}
}

// TestGenerateAndSetTitle_Force_OverridesExplicitNameAndUsesContextTemplate
// verifies the two other key Force semantics (mitto-yv2):
//  1. It uses the dedicated from-context prompt template (GenerateTitleFromContext),
//     not the initial-message template (GenerateTitle) — checked by asserting the
//     composed prompt contains the from-context template's unique wording.
//  2. On success it overrides an existing NameExplicit title (the whole point of
//     an explicit "Auto-rename" action) and keeps NameExplicit=true / clears
//     NameIsFallback so a later internal auto-title retry can't clobber it.
func TestGenerateAndSetTitle_Force_OverridesExplicitNameAndUsesContextTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "force-override"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// Seed an existing explicit title — without Force, GenerateAndSetTitle's
	// SessionNeedsTitle gate and the final NameExplicit guard would both
	// suppress regeneration entirely.
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "Old Explicit Title"
		m.NameExplicit = true
	}); err != nil {
		t.Fatalf("UpdateMetadata (seed explicit title) failed: %v", err)
	}

	provider := &captureAuxProvider{response: `"New Contextual Title"`}
	mgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)

	done := make(chan string, 1)
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:            store,
		SessionID:        sessionID,
		Message:          "User: what about sessions?\nAssistant: use JWT tokens",
		WorkspaceUUID:    "test-workspace",
		AuxiliaryManager: mgr,
		Force:            true,
		OnTitleGenerated: func(_, title string) {
			select {
			case done <- title:
			default:
			}
		},
	})

	select {
	case title := <-done:
		if title != "New Contextual Title" {
			t.Fatalf("OnTitleGenerated title = %q, want %q", title, "New Contextual Title")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnTitleGenerated callback")
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Name != "New Contextual Title" {
		t.Errorf("Name = %q, want %q (Force must override an existing explicit title)", meta.Name, "New Contextual Title")
	}
	if !meta.NameExplicit {
		t.Error("NameExplicit should remain true after a forced regenerate, so a later auto-title retry can't clobber it")
	}
	if meta.NameIsFallback {
		t.Error("NameIsFallback should be false after a successful forced regenerate")
	}

	if !strings.Contains(provider.lastPrompt, "excerpt from a conversation") {
		t.Errorf("Force path should use the dedicated from-context template, got prompt: %q", provider.lastPrompt)
	}
	if provider.lastPurpose != auxiliary.PurposeTitleGen {
		t.Errorf("purpose = %q, want %q", provider.lastPurpose, auxiliary.PurposeTitleGen)
	}
}
