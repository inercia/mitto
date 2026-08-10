package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_PatchLoop_HappyPath(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"prompt":"do it","frequency":{"value":5,"unit":"minutes"},"enabled":false,"triggers":["schedule"]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	enabled := false
	config, err := c.PatchLoop("sess-1", LoopPatchRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("PatchLoop: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotBody["enabled"] != false {
		t.Errorf("request body enabled = %v, want false", gotBody["enabled"])
	}
	if config.Enabled || config.Prompt != "do it" || len(config.Triggers) != 1 {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}

func TestClient_PatchLoop_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.PatchLoop("sess-1", LoopPatchRequest{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_RestoreLoop_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/restore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"prompt":"restored","frequency":{"value":1,"unit":"hours"},"enabled":true}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	config, err := c.RestoreLoop("sess-1")
	if err != nil {
		t.Fatalf("RestoreLoop: %v", err)
	}
	if !config.Enabled || config.Prompt != "restored" {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}

func TestClient_RestoreLoop_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/restore", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.RestoreLoop("sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_RestoreLoop_409_ReturnsTypedConflictError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/restore", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"loop already configured"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.RestoreLoop("sess-1")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is(err, ErrConflict) = false, want true; err = %v", err)
	}
}

func TestClient_AcknowledgeLoopStoppedReason_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/acknowledge-stopped-reason", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"prompt":"p","frequency":{"value":1,"unit":"hours"},"enabled":true,"stopped_reason":""}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	config, err := c.AcknowledgeLoopStoppedReason("sess-1")
	if err != nil {
		t.Fatalf("AcknowledgeLoopStoppedReason: %v", err)
	}
	if config.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", config.StoppedReason)
	}
}

func TestClient_AcknowledgeLoopStoppedReason_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/acknowledge-stopped-reason", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.AcknowledgeLoopStoppedReason("sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_SuggestLoopFromRecent_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/suggest-from-recent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"prompt_name":"nightly-cleanup","frequency":{"value":1,"unit":"days","at":"03:00"},"enabled":false}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	suggestion, err := c.SuggestLoopFromRecent("sess-1")
	if err != nil {
		t.Fatalf("SuggestLoopFromRecent: %v", err)
	}
	if suggestion.PromptName != "nightly-cleanup" || suggestion.Enabled {
		t.Errorf("LoopSuggestion = %+v, unexpected", suggestion)
	}
}

func TestClient_SuggestLoopFromRecent_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop/suggest-from-recent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no suggestion available"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.SuggestLoopFromRecent("sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

// TestClient_SetLoop_MultiTriggerSchema_RoundTrip pins the mitto-r6j.5 design
// decision recorded in the Implementation comment: SetLoopRequest serializes
// the new multi-trigger fields (triggers/child_events/etc.) and LoopConfig
// decodes the server's back-compat "trigger" alongside "triggers".
func TestClient_SetLoop_MultiTriggerSchema_RoundTrip(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"prompt":"p","frequency":{"value":1,"unit":"hours"},"enabled":true,"trigger":"onTasks","triggers":["onTasks","onCompletion"],"child_events":["anyEndResponse"]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	config, err := c.SetLoop("sess-1", SetLoopRequest{
		Prompt:      "p",
		Enabled:     true,
		Triggers:    []string{"onTasks", "onCompletion"},
		ChildEvents: []string{"anyEndResponse"},
	})
	if err != nil {
		t.Fatalf("SetLoop: %v", err)
	}
	triggers, _ := gotBody["triggers"].([]any)
	if len(triggers) != 2 || triggers[0] != "onTasks" {
		t.Errorf("request body triggers = %v, want [onTasks onCompletion]", gotBody["triggers"])
	}
	if _, hasLegacyTrigger := gotBody["trigger"]; hasLegacyTrigger {
		t.Error("request body must not send the legacy scalar 'trigger' field")
	}
	if config.Trigger != "onTasks" || len(config.Triggers) != 2 || len(config.ChildEvents) != 1 {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}
