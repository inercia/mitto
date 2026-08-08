// session_create_no_archive_test.go: REST create-path coverage for
// mitto-yvel.2's reuse no-op guarantee (epic decision 4). The "applied on a
// genuine create" scenario is covered at the MCP layer instead
// (tools_conversation_new_no_archive_test.go) — mirroring the documented
// limitation in session_create_suppress_auto_children_e2e_test.go: this REST
// E2E harness cannot bring up a real ACP server, so a plain (non-reuse) POST
// /api/sessions fails downstream before ever reaching the UpdateMetadata call
// that would persist NoArchive. Reuse-branch tests below return before that
// point, so they succeed and are directly observable here.
package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// TestReuseIssueE2E_HitDoesNotOverwriteNoArchive_Unprotected verifies that a
// reuseIssue hit into an existing UNPROTECTED conversation never applies a
// prompt's target.noArchive:true retroactively — creation-time-only, per
// epic decision 4.
func TestReuseIssueE2E_HitDoesNotOverwriteNoArchive_Unprotected(t *testing.T) {
	const workingDir = "/work-no-archive-reuse-unprotected"
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionID := "20260201-140000-noarchiveA"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workingDir,
		BeadsIssue: "mitto-noarchive-a",
		NoArchive:  false,
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return true }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }
	h.deps.ResolvePromptTarget = func(string, string, map[string]string, string) (ResolvedPromptTarget, error) {
		return ResolvedPromptTarget{NoArchive: true}, nil // would-be default, must NOT apply on reuse
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-noarchive-a",
		OriginPromptName: "protected-prompt",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.NoArchive {
		t.Errorf("NoArchive = true after reuseIssue hit, want unchanged false")
	}
}

// TestReuseIssueE2E_HitDoesNotOverwriteNoArchive_Protected is the mirror
// direction: a reuseIssue hit via a prompt WITHOUT target.noArchive must not
// clear an existing PROTECTED conversation's flag.
func TestReuseIssueE2E_HitDoesNotOverwriteNoArchive_Protected(t *testing.T) {
	const workingDir = "/work-no-archive-reuse-protected"
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionID := "20260201-140000-noarchiveB"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workingDir,
		BeadsIssue: "mitto-noarchive-b",
		NoArchive:  true,
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return true }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }
	h.deps.ResolvePromptTarget = func(string, string, map[string]string, string) (ResolvedPromptTarget, error) {
		return ResolvedPromptTarget{}, nil // no NoArchive here
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-noarchive-b",
		OriginPromptName: "plain-prompt",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if !meta.NoArchive {
		t.Errorf("NoArchive = false after reuseIssue hit, want unchanged true (protected conversation must stay protected)")
	}
}
