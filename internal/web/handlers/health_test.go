package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
)

func TestHandleHealthCheck(t *testing.T) {
	// Create a minimal handlers facade with a session manager
	sm := conversation.NewSessionManager("", "test-server", false, nil)
	h := New(Deps{SessionManager: sm})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()

	h.HandleHealthCheck(rr, req)

	// Check status code
	if rr.Code != http.StatusOK {
		t.Errorf("HandleHealthCheck returned status %d, want %d", rr.Code, http.StatusOK)
	}

	// Check content type
	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Check status field
	if status, ok := response["status"].(string); !ok || status != "healthy" {
		t.Errorf("status = %v, want %q", response["status"], "healthy")
	}

	// Check timestamp field exists
	if _, ok := response["timestamp"]; !ok {
		t.Error("Response should contain timestamp field")
	}

	// Check sessions field exists
	if sessions, ok := response["sessions"].(map[string]interface{}); !ok {
		t.Error("Response should contain sessions field")
	} else {
		if _, ok := sessions["active"]; !ok {
			t.Error("sessions should contain active field")
		}
		if _, ok := sessions["prompting"]; !ok {
			t.Error("sessions should contain prompting field")
		}
	}
}

func TestHandleHealthCheck_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	// Create a POST request (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/api/health", nil)
	rr := httptest.NewRecorder()

	h.HandleHealthCheck(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleHealthCheck with POST returned status %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleHealthCheck_Shutdown(t *testing.T) {
	h := New(Deps{IsShutdown: func() bool { return true }})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()

	h.HandleHealthCheck(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("HandleHealthCheck during shutdown returned status %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if status, ok := response["status"].(string); !ok || status != "unhealthy" {
		t.Errorf("status = %v, want %q", response["status"], "unhealthy")
	}
}

// --- HandleAuthInfo (mitto-4mz.2: passkey availability) ---

func TestHandleAuthInfo_NilAuthInfoReportsAllFalse(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/auth-info", nil)
	rr := httptest.NewRecorder()
	h.HandleAuthInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var info map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	want := map[string]bool{"simple": false, "cloudflare": false, "passkey": false}
	for k, v := range want {
		if info[k] != v {
			t.Errorf("info[%q] = %v, want %v", k, info[k], v)
		}
	}
}

func TestHandleAuthInfo_ReportsAllThreeMethods(t *testing.T) {
	h := New(Deps{
		AuthInfo: func() (bool, bool, bool) { return true, false, true },
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth-info", nil)
	rr := httptest.NewRecorder()
	h.HandleAuthInfo(rr, req)

	var info map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !info["simple"] {
		t.Error("info[simple] = false, want true")
	}
	if info["cloudflare"] {
		t.Error("info[cloudflare] = true, want false")
	}
	if !info["passkey"] {
		t.Error("info[passkey] = false, want true")
	}
}

func TestHandleAuthInfo_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/auth-info", nil)
	rr := httptest.NewRecorder()
	h.HandleAuthInfo(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}
