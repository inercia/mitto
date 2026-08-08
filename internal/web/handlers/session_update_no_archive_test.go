package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestHandleUpdateSession_NoArchiveIsImmutable pins epic decision 3
// (mitto-yvel): PATCH /api/sessions/{id} has no no_archive field on
// SessionUpdateRequest, so a request body carrying "no_archive" is silently
// ignored by encoding/json — in both directions. An unprotected conversation
// stays unprotected when the PATCH tries to set no_archive:true, and a
// protected conversation stays protected when the PATCH tries to clear it
// with no_archive:false (the direction that would catch an accidental
// mutation path being added later).
func TestHandleUpdateSession_NoArchiveIsImmutable(t *testing.T) {
	t.Run("PATCH no_archive:true on an unprotected session leaves it false", func(t *testing.T) {
		sid := "test-session-no-archive-set"
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{
			SessionID:  sid,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
			NoArchive:  false,
		}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"no_archive": true, "name": "renamed"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata failed: %v", err)
		}
		if meta.NoArchive {
			t.Errorf("NoArchive = true after PATCH no_archive:true, want unchanged false (immutable)")
		}
		if meta.Name != "renamed" {
			t.Errorf("Name = %q, want %q (sanity: other fields in the same PATCH still apply)", meta.Name, "renamed")
		}
	})

	t.Run("PATCH no_archive:false on a protected session leaves it true", func(t *testing.T) {
		sid := "test-session-no-archive-clear"
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{
			SessionID:  sid,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
			NoArchive:  true,
		}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"no_archive": false}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata failed: %v", err)
		}
		if !meta.NoArchive {
			t.Errorf("NoArchive = false after PATCH no_archive:false, want unchanged true (immutable, no unprotect escape hatch)")
		}
	})
}

// TestHandleUpdateSession_NoArchiveRejectsArchive pins mitto-yvel.3: PATCH
// archived:true on a NoArchive conversation is rejected with 409 conflict and
// leaves the conversation's Archived/ArchivedAt/ArchiveReason untouched, while
// unarchive, delete, and unrelated PATCH fields on the SAME conversation stay
// fully functional (only archiving is gated, per epic decision 3).
func TestHandleUpdateSession_NoArchiveRejectsArchive(t *testing.T) {
	t.Run("PATCH archived:true on a NoArchive session is rejected with 409 conflict", func(t *testing.T) {
		sid := "test-session-no-archive-reject"
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{
			SessionID:  sid,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
			NoArchive:  true,
		}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"archived": true}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, sid)

		if w.Code != http.StatusConflict {
			t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to decode error body: %v; body: %s", err, w.Body.String())
		}
		if envelope.Error.Code != errCodeConflict {
			t.Errorf("error.code = %q, want %q", envelope.Error.Code, errCodeConflict)
		}

		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata failed: %v", err)
		}
		if meta.Archived {
			t.Error("Archived = true after rejected PATCH, want unchanged false")
		}
		if !meta.ArchivedAt.IsZero() {
			t.Errorf("ArchivedAt = %v after rejected PATCH, want zero", meta.ArchivedAt)
		}
		if meta.ArchiveReason != "" {
			t.Errorf("ArchiveReason = %q after rejected PATCH, want empty", meta.ArchiveReason)
		}
	})

	t.Run("PATCH archived:false (unarchive) on a NoArchive session still works", func(t *testing.T) {
		sid := "test-session-no-archive-unarchive-ok"
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{
			SessionID:  sid,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
			NoArchive:  true,
		}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		// Pre-condition: pretend the session somehow ended up archived (e.g.
		// legacy data predating this guard) — unarchive must still clear it.
		if err := store.UpdateMetadata(sid, func(m *session.Metadata) {
			m.Archived = true
			m.ArchiveReason = session.ArchiveReasonManual
		}); err != nil {
			t.Fatalf("UpdateMetadata (seed archived) failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"archived": false}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, sid)

		if w.Code != http.StatusOK {
			t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata failed: %v", err)
		}
		if meta.Archived {
			t.Error("Archived = true after unarchive PATCH, want false")
		}
	})

	t.Run("PATCH name on a NoArchive session (no archived field) still works", func(t *testing.T) {
		sid := "test-session-no-archive-rename-ok"
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{
			SessionID:  sid,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
			NoArchive:  true,
		}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"name": "renamed"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, sid)

		if w.Code != http.StatusOK {
			t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata failed: %v", err)
		}
		if meta.Name != "renamed" {
			t.Errorf("Name = %q, want %q (non-archive PATCH fields must be unaffected by the guard)", meta.Name, "renamed")
		}
	})

	t.Run("PATCH archived:true on a NoArchive CHILD session still redirects to delete", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore failed: %v", err)
		}
		t.Cleanup(func() { store.Close() })

		parentID := "test-parent-for-no-archive-child"
		if err := store.Create(session.Metadata{
			SessionID:  parentID,
			ACPServer:  "test-server",
			WorkingDir: t.TempDir(),
		}); err != nil {
			t.Fatalf("Create parent failed: %v", err)
		}
		childID := "test-no-archive-child"
		if err := store.Create(session.Metadata{
			SessionID:       childID,
			ACPServer:       "test-server",
			WorkingDir:      t.TempDir(),
			ParentSessionID: parentID,
			NoArchive:       true,
		}); err != nil {
			t.Fatalf("Create child failed: %v", err)
		}
		h := New(Deps{Store: store})

		body := `{"archived": true}`
		req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+childID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleUpdateSession(w, req, childID)

		// The child redirect fires before the NoArchive guard, so this must
		// behave exactly like deleting any other child: 204 (via
		// HandleDeleteSession), and the child is gone (deletion stays fully
		// allowed for a protected conversation, epic decision 3) — NOT a 409.
		if w.Code != http.StatusNoContent {
			t.Fatalf("Status = %d, want %d (child redirect to delete); body: %s", w.Code, http.StatusNoContent, w.Body.String())
		}
		if _, err := store.GetMetadata(childID); err == nil {
			t.Error("Expected NoArchive child to be deleted via the archive-to-delete redirect, but it still exists")
		}
	})
}
