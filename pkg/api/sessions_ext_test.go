package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_UpdateSession_HappyPath(t *testing.T) {
	var gotMethod, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := json.Marshal(map[string]any{})
		_ = json.NewDecoder(r.Body).Decode(&b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session_id":"sess-1","acp_server":"auggie","working_dir":"/tmp","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","event_count":3,"status":"idle","name":"renamed"}`))
		gotBody = "seen"
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	name := "renamed"
	meta, err := c.UpdateSession("sess-1", SessionUpdateRequest{Name: &name})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotBody == "" {
		t.Error("server never saw a request body")
	}
	if meta.SessionID != "sess-1" || meta.Name != "renamed" || meta.Status != "idle" {
		t.Errorf("meta = %+v, want session_id=sess-1 name=renamed status=idle", meta)
	}
}

func TestClient_UpdateSession_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	name := "x"
	_, err := c.UpdateSession("missing", SessionUpdateRequest{Name: &name})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_GetSessionEvents_HappyPath_WithOptions(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"seq":1,"type":"user_message","timestamp":"2026-01-01T00:00:00Z","data":{"text":"hi"}}]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	events, err := c.GetSessionEvents("sess-1", GetSessionEventsOptions{Limit: 10, BeforeSeq: 5, Reverse: true})
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if gotQuery != "before=5&limit=10&order=desc" {
		t.Errorf("query = %q, want before=5&limit=10&order=desc", gotQuery)
	}
	if len(events) != 1 || events[0].Seq != 1 || events[0].Type != "user_message" {
		t.Errorf("events = %+v, want one user_message event with seq=1", events)
	}
}

func TestClient_GetSessionEvents_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/missing/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.GetSessionEvents("missing", GetSessionEventsOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_GetSessionChanges_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"files":[{"path":"a.go","status":"M","additions":3,"deletions":1}],"is_git_repo":true,"branch":"main"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	changes, err := c.GetSessionChanges("sess-1")
	if err != nil {
		t.Fatalf("GetSessionChanges: %v", err)
	}
	if !changes.IsGitRepo || changes.Branch != "main" || len(changes.Files) != 1 || changes.Files[0].Path != "a.go" {
		t.Errorf("changes = %+v, unexpected", changes)
	}
}

func TestClient_GetSessionSettings_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"settings":{"beta_feature":true}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.GetSessionSettings("sess-1")
	if err != nil {
		t.Fatalf("GetSessionSettings: %v", err)
	}
	if !got.Settings["beta_feature"] {
		t.Errorf("Settings = %+v, want beta_feature=true", got.Settings)
	}
}

func TestClient_UpdateSessionSettings_HappyPath(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/settings", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"settings":{"beta_feature":false}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.UpdateSessionSettings("sess-1", map[string]bool{"beta_feature": false})
	if err != nil {
		t.Fatalf("UpdateSessionSettings: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	settings, _ := gotBody["settings"].(map[string]any)
	if settings["beta_feature"] != false {
		t.Errorf("request body settings = %+v, want beta_feature=false", settings)
	}
	if got.Settings["beta_feature"] {
		t.Errorf("Settings = %+v, want beta_feature=false", got.Settings)
	}
}

func TestClient_FlushSession_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/flush", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"flushed","command":"/clear"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.FlushSession("sess-1")
	if err != nil {
		t.Fatalf("FlushSession: %v", err)
	}
	if got.Status != "flushed" || got.Command != "/clear" {
		t.Errorf("FlushResponse = %+v, unexpected", got)
	}
}

func TestClient_FlushSession_409_ReturnsTypedConflictError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/flush", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"session is busy"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.FlushSession("sess-1")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is(err, ErrConflict) = false, want true; err = %v", err)
	}
}

func TestClient_GetSessionUserData_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/user-data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"attributes":[{"name":"priority","value":"high"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.GetSessionUserData("sess-1")
	if err != nil {
		t.Fatalf("GetSessionUserData: %v", err)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Name != "priority" || got.Attributes[0].Value != "high" {
		t.Errorf("UserData = %+v, unexpected", got)
	}
}

func TestClient_SetSessionUserData_HappyPath(t *testing.T) {
	var gotMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/user-data", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"attributes":[{"name":"priority","value":"low"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.SetSessionUserData("sess-1", []UserDataAttribute{{Name: "priority", Value: "low"}})
	if err != nil {
		t.Fatalf("SetSessionUserData: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Value != "low" {
		t.Errorf("UserData = %+v, unexpected", got)
	}
}

func TestClient_SetSessionUserData_400_ReturnsTypedBadRequestError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/user-data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"validation_error","message":"unknown attribute"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.SetSessionUserData("sess-1", []UserDataAttribute{{Name: "bogus", Value: "x"}})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is(err, ErrBadRequest) = false, want true; err = %v", err)
	}
}

func TestClient_PruneSession_HappyPath(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/prune", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pruned_count":50,"remaining_count":100,"new_max_seq":150}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.PruneSession("sess-1", 100)
	if err != nil {
		t.Fatalf("PruneSession: %v", err)
	}
	if gotBody["keep_last"] != float64(100) {
		t.Errorf("request body keep_last = %v, want 100", gotBody["keep_last"])
	}
	if got.PrunedCount != 50 || got.RemainingCount != 100 || got.NewMaxSeq != 150 {
		t.Errorf("PruneResponse = %+v, unexpected", got)
	}
}

func TestClient_PruneSession_409_ReturnsTypedConflictError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/prune", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"session is busy"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.PruneSession("sess-1", 100)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is(err, ErrConflict) = false, want true; err = %v", err)
	}
}

func TestClient_ListRunningSessions_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/running", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"total_running":2,"prompting":1,"sessions":[{"session_id":"a","is_prompting":true},{"session_id":"b","is_prompting":false}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.ListRunningSessions()
	if err != nil {
		t.Fatalf("ListRunningSessions: %v", err)
	}
	if got.TotalRunning != 2 || got.Prompting != 1 || len(got.Sessions) != 2 {
		t.Errorf("RunningSessionsResponse = %+v, unexpected", got)
	}
}
