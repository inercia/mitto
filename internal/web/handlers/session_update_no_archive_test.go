package handlers

import (
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
