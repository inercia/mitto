package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestClient_UpdateSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1").
		RespondJSON(http.StatusOK, `{"session_id":"sess-1","acp_server":"auggie","working_dir":"/tmp","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","event_count":3,"status":"idle","name":"renamed"}`)

	name := "renamed"
	meta, err := f.Client().UpdateSession("sess-1", SessionUpdateRequest{Name: &name})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	req := f.LastRequest()
	if req.Method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", req.Method)
	}
	if len(req.Body) == 0 {
		t.Error("server never saw a request body")
	}
	if meta.SessionID != "sess-1" || meta.Name != "renamed" || meta.Status != "idle" {
		t.Errorf("meta = %+v, want session_id=sess-1 name=renamed status=idle", meta)
	}
}

// TestClient_UpdateSession_DecodesFullMetadataShape guards the review-phase
// fix for SessionMetadata omitting fields the server's session.Metadata emits
// (is_auto_child, auto_unarchive_last_attempt_at), which were silently dropped
// on decode.
func TestClient_UpdateSession_DecodesFullMetadataShape(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1").RespondJSON(http.StatusOK,
		`{"session_id":"sess-1","acp_server":"auggie","working_dir":"/tmp","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","event_count":0,"status":"idle","is_auto_child":true,"child_origin":"auto","auto_unarchive_last_attempt_at":"2026-08-10T06:00:00Z"}`)

	meta, err := f.Client().UpdateSession("sess-1", SessionUpdateRequest{})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if !meta.IsAutoChild || meta.ChildOrigin != "auto" {
		t.Errorf("child fields = %v/%q, want true/auto", meta.IsAutoChild, meta.ChildOrigin)
	}
	if meta.AutoUnarchiveLastAttemptAt.IsZero() {
		t.Error("AutoUnarchiveLastAttemptAt not decoded")
	}
}

func TestClient_UpdateSession_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/missing").RespondRaw(http.StatusNotFound, "", nil)

	name := "x"
	_, err := f.Client().UpdateSession("missing", SessionUpdateRequest{Name: &name})
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_GetSessionEvents_HappyPath_WithOptions(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/events").
		RespondJSON(http.StatusOK, `[{"seq":1,"type":"user_message","timestamp":"2026-01-01T00:00:00Z","data":{"text":"hi"}}]`)

	events, err := f.Client().GetSessionEvents("sess-1", GetSessionEventsOptions{Limit: 10, BeforeSeq: 5, Reverse: true})
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if got := f.LastRequest().RawQuery; got != "before=5&limit=10&order=desc" {
		t.Errorf("query = %q, want before=5&limit=10&order=desc", got)
	}
	if len(events) != 1 || events[0].Seq != 1 || events[0].Type != "user_message" {
		t.Errorf("events = %+v, want one user_message event with seq=1", events)
	}
}

func TestClient_GetSessionEvents_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/missing/events").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().GetSessionEvents("missing", GetSessionEventsOptions{})
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_GetSessionChanges_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/changes").
		RespondJSON(http.StatusOK, `{"files":[{"path":"a.go","status":"M","additions":3,"deletions":1}],"is_git_repo":true,"branch":"main"}`)

	changes, err := f.Client().GetSessionChanges("sess-1")
	if err != nil {
		t.Fatalf("GetSessionChanges: %v", err)
	}
	if !changes.IsGitRepo || changes.Branch != "main" || len(changes.Files) != 1 || changes.Files[0].Path != "a.go" {
		t.Errorf("changes = %+v, unexpected", changes)
	}
}

func TestClient_GetSessionSettings_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/settings").RespondJSON(http.StatusOK, `{"settings":{"beta_feature":true}}`)

	got, err := f.Client().GetSessionSettings("sess-1")
	if err != nil {
		t.Fatalf("GetSessionSettings: %v", err)
	}
	if !got.Settings["beta_feature"] {
		t.Errorf("Settings = %+v, want beta_feature=true", got.Settings)
	}
}

func TestClient_UpdateSessionSettings_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1/settings").RespondJSON(http.StatusOK, `{"settings":{"beta_feature":false}}`)

	got, err := f.Client().UpdateSessionSettings("sess-1", map[string]bool{"beta_feature": false})
	if err != nil {
		t.Fatalf("UpdateSessionSettings: %v", err)
	}
	req := f.LastRequest()
	if req.Method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", req.Method)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(req.Body, &gotBody)
	settings, _ := gotBody["settings"].(map[string]any)
	if settings["beta_feature"] != false {
		t.Errorf("request body settings = %+v, want beta_feature=false", settings)
	}
	if got.Settings["beta_feature"] {
		t.Errorf("Settings = %+v, want beta_feature=false", got.Settings)
	}
}

// TestClient_UpdateSessionSettings_404_ReturnsTypedNotFoundError pins the 404
// short-circuit (sessionNotFoundError), which the shared negative-path matrix
// does not reach: it only exercises 5xx statuses.
func TestClient_UpdateSessionSettings_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/missing/settings").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().UpdateSessionSettings("missing", map[string]bool{"beta_feature": true})
	apiErr := assertAPIError(t, err, ErrNotFound, http.StatusNotFound, CodeNotFound)
	if apiErr.Details["session_id"] != "missing" {
		t.Errorf("Details[session_id] = %v, want %q", apiErr.Details["session_id"], "missing")
	}
}

func TestClient_FlushSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/flush").RespondJSON(http.StatusOK, `{"status":"flushed","command":"/clear"}`)

	got, err := f.Client().FlushSession("sess-1")
	if err != nil {
		t.Fatalf("FlushSession: %v", err)
	}
	if got.Status != "flushed" || got.Command != "/clear" {
		t.Errorf("FlushResponse = %+v, unexpected", got)
	}
}

func TestClient_FlushSession_409_ReturnsTypedConflictError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/flush").
		Fail(http.StatusConflict, "conflict", "session is busy", nil)

	_, err := f.Client().FlushSession("sess-1")
	assertAPIError(t, err, ErrConflict, http.StatusConflict, "conflict")
}

func TestClient_GetSessionUserData_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/user-data").
		RespondJSON(http.StatusOK, `{"attributes":[{"name":"priority","value":"high"}]}`)

	got, err := f.Client().GetSessionUserData("sess-1")
	if err != nil {
		t.Fatalf("GetSessionUserData: %v", err)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Name != "priority" || got.Attributes[0].Value != "high" {
		t.Errorf("UserData = %+v, unexpected", got)
	}
}

// TestClient_GetSessionUserData_404_ReturnsTypedNotFoundError pins the 404
// short-circuit (sessionNotFoundError), which the shared negative-path matrix
// does not reach: it only exercises 5xx statuses.
func TestClient_GetSessionUserData_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/missing/user-data").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().GetSessionUserData("missing")
	apiErr := assertAPIError(t, err, ErrNotFound, http.StatusNotFound, CodeNotFound)
	if apiErr.Details["session_id"] != "missing" {
		t.Errorf("Details[session_id] = %v, want %q", apiErr.Details["session_id"], "missing")
	}
}

func TestClient_SetSessionUserData_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPut, "/mitto/api/sessions/sess-1/user-data").
		RespondJSON(http.StatusOK, `{"attributes":[{"name":"priority","value":"low"}]}`)

	got, err := f.Client().SetSessionUserData("sess-1", []UserDataAttribute{{Name: "priority", Value: "low"}})
	if err != nil {
		t.Fatalf("SetSessionUserData: %v", err)
	}
	if got := f.LastRequest().Method; got != http.MethodPut {
		t.Errorf("method = %q, want PUT", got)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Value != "low" {
		t.Errorf("UserData = %+v, unexpected", got)
	}
}

func TestClient_SetSessionUserData_400_ReturnsTypedBadRequestError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPut, "/mitto/api/sessions/sess-1/user-data").
		Fail(http.StatusBadRequest, "validation_error", "unknown attribute", nil)

	_, err := f.Client().SetSessionUserData("sess-1", []UserDataAttribute{{Name: "bogus", Value: "x"}})
	assertAPIError(t, err, ErrBadRequest, http.StatusBadRequest, "validation_error")
}

func TestClient_PruneSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/prune").
		RespondJSON(http.StatusOK, `{"pruned_count":50,"remaining_count":100,"new_max_seq":150}`)

	got, err := f.Client().PruneSession("sess-1", 100)
	if err != nil {
		t.Fatalf("PruneSession: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	if gotBody["keep_last"] != float64(100) {
		t.Errorf("request body keep_last = %v, want 100", gotBody["keep_last"])
	}
	if got.PrunedCount != 50 || got.RemainingCount != 100 || got.NewMaxSeq != 150 {
		t.Errorf("PruneResponse = %+v, unexpected", got)
	}
}

func TestClient_PruneSession_409_ReturnsTypedConflictError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/prune").
		Fail(http.StatusConflict, "conflict", "session is busy", nil)

	_, err := f.Client().PruneSession("sess-1", 100)
	assertAPIError(t, err, ErrConflict, http.StatusConflict, "conflict")
}

// --- Coverage for previously-0%-tested core Client methods (mitto-rwxq.9) ---

func TestClient_CreateSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions").
		RespondRaw(http.StatusCreated, "application/json", []byte(`{"session_id":"sess-1","working_dir":"/tmp","acp_server":"auggie"}`))

	got, err := f.Client().CreateSession(CreateSessionRequest{Name: "n", WorkingDir: "/tmp"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("SessionInfo = %+v, unexpected", got)
	}
}

func TestClient_CreateSession_500_ReturnsTypedAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions").
		Fail(http.StatusInternalServerError, CodeServerError, "boom", nil)

	_, err := f.Client().CreateSession(CreateSessionRequest{Name: "n", WorkingDir: "/tmp"})
	assertAPIError(t, err, ErrServerError, http.StatusInternalServerError, CodeServerError)
}

func TestClient_ListSessions_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions").
		RespondJSON(http.StatusOK, `[{"session_id":"sess-1","acp_server":"auggie","parent_session_id":"parent-1","child_origin":"mcp"},{"session_id":"sess-2","acp_server":"claude"}]`)

	got, err := f.Client().ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 || got[0].SessionID != "sess-1" {
		t.Errorf("ListSessions = %+v, unexpected", got)
	}
	if got[0].ParentSessionID != "parent-1" || got[0].ChildOrigin != "mcp" {
		t.Errorf("ListSessions session relationship fields = %+v, want parent-1/mcp", got[0])
	}
}

func TestClient_ListSessions_500_ReturnsTypedAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions").
		Fail(http.StatusInternalServerError, CodeServerError, "boom", nil)

	_, err := f.Client().ListSessions()
	assertAPIError(t, err, ErrServerError, http.StatusInternalServerError, CodeServerError)
}

func TestClient_DeleteSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1").RespondRaw(http.StatusNoContent, "", nil)

	if err := f.Client().DeleteSession("sess-1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
}

func TestClient_DeleteSession_404_ReturnsGenericAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/missing").RespondRaw(http.StatusNotFound, "", nil)

	err := f.Client().DeleteSession("missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_ArchiveSession_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1").RespondRaw(http.StatusOK, "", nil)

	if err := f.Client().ArchiveSession("sess-1", true); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	if gotBody["archived"] != true {
		t.Errorf("request body archived = %v, want true", gotBody["archived"])
	}
}

func TestClient_ArchiveSession_404_ReturnsTypedAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/missing").RespondRaw(http.StatusNotFound, "", nil)

	err := f.Client().ArchiveSession("missing", true)
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_BaseURL(t *testing.T) {
	c := New("http://example.invalid")
	if got := c.BaseURL(); got != "http://example.invalid" {
		t.Errorf("BaseURL() = %q, want http://example.invalid", got)
	}
}

func TestWithTimeout_SetsHTTPClientTimeout(t *testing.T) {
	c := New("http://example.invalid", WithTimeout(5*time.Second))
	if c.httpClient.Timeout != 5*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 5s", c.httpClient.Timeout)
	}
}

func TestClient_RotateSharedToken_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/auth/rotate-token").RespondJSON(http.StatusOK, `{"fingerprint":"abc123"}`)

	got, err := f.Client().RotateSharedToken()
	if err != nil {
		t.Fatalf("RotateSharedToken: %v", err)
	}
	if got.Fingerprint != "abc123" {
		t.Errorf("RotateTokenResponse = %+v, unexpected", got)
	}
}

func TestClient_RotateSharedToken_403_ReturnsTypedForbiddenError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/auth/rotate-token").
		Fail(http.StatusForbidden, CodeForbidden, "not localhost", nil)

	_, err := f.Client().RotateSharedToken()
	assertAPIError(t, err, ErrForbidden, http.StatusForbidden, CodeForbidden)
}

func TestClient_ListRunningSessions_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/running").
		RespondJSON(http.StatusOK, `{"total_running":2,"prompting":1,"sessions":[{"session_id":"a","is_prompting":true},{"session_id":"b","is_prompting":false}]}`)

	got, err := f.Client().ListRunningSessions()
	if err != nil {
		t.Fatalf("ListRunningSessions: %v", err)
	}
	if got.TotalRunning != 2 || got.Prompting != 1 || len(got.Sessions) != 2 {
		t.Errorf("RunningSessionsResponse = %+v, unexpected", got)
	}
}
