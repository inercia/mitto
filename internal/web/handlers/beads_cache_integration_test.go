package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// statefulBeadsClient is a beads.Client that keeps a single issue's status in
// memory. List returns a JSON payload derived from that status; SetStatus
// mutates it based on the action verb. Every List call bumps listCalls so
// tests can assert the outer CachingClient actually reached this inner client
// (vs. serving from cache).
type statefulBeadsClient struct {
	stubBeadsClient
	mu        sync.Mutex
	status    string
	listCalls atomic.Int64
}

func (s *statefulBeadsClient) List(_ context.Context, _ string) ([]byte, error) {
	s.listCalls.Add(1)
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	return []byte(fmt.Sprintf(`[{"id":"x-1","status":%q}]`, st)), nil
}

func (s *statefulBeadsClient) SetStatus(_ context.Context, _, _, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch action {
	case "close":
		s.status = "closed"
	case "reopen":
		s.status = "open"
	}
	return nil
}

// TestCachingClient_WriteReadThrough_StatusVisibleImmediately is the mitto-is2.4
// end-to-end acceptance test: a POST /api/issues/{id}/status?action=close
// immediately followed by a GET /api/issues must return the just-closed status,
// with no sleep and no fsnotify involvement. Passes only because CachingClient's
// SetStatus writer defers Invalidate(dir), dropping the workspace's cached list
// payload before the next read is served.
func TestCachingClient_WriteReadThrough_StatusVisibleImmediately(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".beads", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	inner := &statefulBeadsClient{status: "open"}
	cache := beads.NewCachingClient(inner)

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: workDir, ACPServer: "test-server"},
	})
	h := New(Deps{SessionManager: sm, BeadsClient: cache})

	// 1. Prime the cache with an initial List showing status=open.
	req1 := localhostRequest("/api/issues?working_dir=" + workDir)
	rec1 := httptest.NewRecorder()
	h.HandleBeadsList(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("initial List: want 200, got %d body=%s", rec1.Code, rec1.Body.String())
	}
	if !strings.Contains(rec1.Body.String(), `"status":"open"`) {
		t.Fatalf("initial List body missing open status: %s", rec1.Body.String())
	}
	firstListCalls := inner.listCalls.Load()
	if firstListCalls != 1 {
		t.Fatalf("after priming List want listCalls=1, got %d", firstListCalls)
	}

	// Sanity: a second immediate List is a cache hit (inner not invoked again).
	req2 := localhostRequest("/api/issues?working_dir=" + workDir)
	rec2 := httptest.NewRecorder()
	h.HandleBeadsList(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cache-hit List: want 200, got %d", rec2.Code)
	}
	if inner.listCalls.Load() != firstListCalls {
		t.Fatalf("cache-hit List should not reach inner; listCalls=%d want=%d",
			inner.listCalls.Load(), firstListCalls)
	}

	// 2. POST /api/issues/x-1/status with {"action":"close"}.
	body := bytes.NewBufferString(`{"action":"close"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/api/issues/x-1/status?working_dir="+workDir, body)
	postReq.RemoteAddr = "127.0.0.1:54321"
	postReq.SetPathValue("id", "x-1")
	postRec := httptest.NewRecorder()
	h.HandleBeadsStatus(postRec, postReq)
	if postRec.Code < 200 || postRec.Code >= 300 {
		t.Fatalf("SetStatus POST: want 2xx, got %d body=%s", postRec.Code, postRec.Body.String())
	}

	// 3. Immediately re-list. Cache must have been invalidated by the writer
	// (defer c.Invalidate(dir) inside CachingClient.SetStatus), so the read
	// reaches inner again and returns the mutated status.
	req3 := localhostRequest("/api/issues?working_dir=" + workDir)
	rec3 := httptest.NewRecorder()
	h.HandleBeadsList(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("post-write List: want 200, got %d body=%s", rec3.Code, rec3.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(rec3.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode List body: %v (body=%s)", err, rec3.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("want 1 issue, got %d (body=%s)", len(items), rec3.Body.String())
	}
	if got, _ := items[0]["status"].(string); got != "closed" {
		t.Fatalf("want status=closed, got %q (body=%s)", got, rec3.Body.String())
	}
	if got := inner.listCalls.Load(); got <= firstListCalls {
		t.Fatalf("post-write List should have reached inner: listCalls=%d, want > %d",
			got, firstListCalls)
	}
}
