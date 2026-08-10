package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestErrorCodeForStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, CodeBadRequest},
		{http.StatusUnauthorized, CodeUnauthenticated},
		{http.StatusForbidden, CodeForbidden},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{http.StatusConflict, CodeConflict},
		{http.StatusRequestEntityTooLarge, CodeTooLarge},
		{http.StatusTooManyRequests, CodeRateLimited},
		{http.StatusServiceUnavailable, CodeUnavailable},
		{http.StatusInternalServerError, CodeServerError},
		{http.StatusTeapot, CodeServerError}, // unmapped status falls back to server_error
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.status), func(t *testing.T) {
			if got := errorCodeForStatus(tt.status); got != tt.want {
				t.Errorf("errorCodeForStatus(%d) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestErrorFromResponse(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name:        "canonical envelope with details",
			status:      http.StatusConflict,
			body:        `{"error":{"code":"queue_full","message":"Queue is full. Maximum 5 messages allowed.","details":{"max":5}}}`,
			wantCode:    "queue_full",
			wantMessage: "Queue is full. Maximum 5 messages allowed.",
			wantDetails: map[string]any{"max": float64(5)},
		},
		{
			name:        "canonical envelope without details",
			status:      http.StatusNotFound,
			body:        `{"error":{"code":"not_found","message":"Session not found"}}`,
			wantCode:    "not_found",
			wantMessage: "Session not found",
		},
		{
			name:        "legacy flat shape",
			status:      http.StatusConflict,
			body:        `{"error":"queue_full","message":"Queue is full."}`,
			wantCode:    "queue_full",
			wantMessage: "Queue is full.",
		},
		{
			name:        "legacy flat shape without message falls back to code",
			status:      http.StatusBadRequest,
			body:        `{"error":"bad_request"}`,
			wantCode:    "bad_request",
			wantMessage: "bad_request",
		},
		{
			name:        "top-level message only, no error field",
			status:      http.StatusInternalServerError,
			body:        `{"message":"just a message"}`,
			wantCode:    CodeServerError,
			wantMessage: "just a message",
		},
		{
			name:        "empty body falls back to status-derived code and generic message",
			status:      http.StatusServiceUnavailable,
			body:        ``,
			wantCode:    CodeUnavailable,
			wantMessage: "request failed with status 503",
		},
		{
			name:        "non-JSON body falls back to status-derived code and generic message",
			status:      http.StatusBadGateway,
			body:        `not json at all`,
			wantCode:    CodeServerError,
			wantMessage: "request failed with status 502",
		},
		{
			name:        "JSON array body is not a valid envelope",
			status:      http.StatusInternalServerError,
			body:        `[1,2,3]`,
			wantCode:    CodeServerError,
			wantMessage: "request failed with status 500",
		},
		{
			name:        "nested error null falls back to top-level message",
			status:      http.StatusInternalServerError,
			body:        `{"error":null,"message":"top-level fallback"}`,
			wantCode:    CodeServerError,
			wantMessage: "top-level fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorFromResponse("op", tt.status, []byte(tt.body))
			if got == nil {
				t.Fatal("errorFromResponse returned nil")
			}
			if got.Status != tt.status {
				t.Errorf("Status = %d, want %d", got.Status, tt.status)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tt.wantCode)
			}
			if got.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", got.Message, tt.wantMessage)
			}
			if !reflect.DeepEqual(got.Details, tt.wantDetails) {
				t.Errorf("Details = %#v, want %#v", got.Details, tt.wantDetails)
			}
			if !bytes.Equal(got.Body, []byte(tt.body)) {
				t.Errorf("Body = %q, want %q (raw body must always be preserved)", got.Body, tt.body)
			}
		})
	}
}

func TestAPIError_Error(t *testing.T) {
	withOp := &APIError{Op: "get session", Status: 404, Code: "not_found", Message: "Session not found"}
	if got, want := withOp.Error(), "get session: status 404 (not_found): Session not found"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	withoutOp := &APIError{Status: 404, Code: "not_found", Message: "Session not found"}
	if got, want := withoutOp.Error(), "status 404 (not_found): Session not found"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestAPIError_Is_MatchesByStatus(t *testing.T) {
	sentinels := []struct {
		name string
		err  *APIError
	}{
		{"ErrBadRequest", ErrBadRequest},
		{"ErrUnauthenticated", ErrUnauthenticated},
		{"ErrForbidden", ErrForbidden},
		{"ErrNotFound", ErrNotFound},
		{"ErrConflict", ErrConflict},
		{"ErrTooLarge", ErrTooLarge},
		{"ErrRateLimited", ErrRateLimited},
		{"ErrUnavailable", ErrUnavailable},
		{"ErrServerError", ErrServerError},
	}

	for _, s := range sentinels {
		t.Run(s.name, func(t *testing.T) {
			// Same status, different (app-specific) code and op: still matches.
			match := &APIError{Op: "some op", Status: s.err.Status, Code: "app_specific_code", Message: "whatever"}
			if !errors.Is(match, s.err) {
				t.Errorf("errors.Is(status=%d, code=app_specific_code, %s) = false, want true", s.err.Status, s.name)
			}

			// Different status: never matches, even carrying the sentinel's own code.
			mismatch := &APIError{Status: s.err.Status + 1, Code: s.err.Code}
			if errors.Is(mismatch, s.err) {
				t.Errorf("errors.Is(status=%d, code=%s, %s) = true, want false", mismatch.Status, s.err.Code, s.name)
			}
		})
	}
}

func TestAPIError_Is_QueueFullMatchesErrConflict(t *testing.T) {
	// Pins the documented deviation from the plan (mitto-rwxq.3 Implementation
	// comment): the real 409 handler (internal/web/handlers/queue.go) attaches
	// the app-specific code "queue_full" rather than the canonical "conflict",
	// so Is() must match by Status, not Code, for this guarantee to hold.
	err := errorFromResponse("add to queue", http.StatusConflict,
		[]byte(`{"error":{"code":"queue_full","message":"Queue is full. Maximum 5 messages allowed."}}`))
	if !errors.Is(err, ErrConflict) {
		t.Error("errors.Is(queue_full 409, ErrConflict) = false, want true")
	}
	if err.Code != "queue_full" {
		t.Errorf("Code = %q, want %q (app-specific code must be preserved)", err.Code, "queue_full")
	}
}

func TestAPIError_As(t *testing.T) {
	var err error = errorFromResponse("get session", http.StatusNotFound,
		[]byte(`{"error":{"code":"not_found","message":"Session not found","details":{"session_id":"abc"}}}`))

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed to extract *APIError")
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", apiErr.Status, http.StatusNotFound)
	}
	if apiErr.Details["session_id"] != "abc" {
		t.Errorf("Details[session_id] = %v, want %q", apiErr.Details["session_id"], "abc")
	}
}

// --- httptest round trips through real client call sites, proving apiError /
// errorFromResponse are actually wired into client.go's call sites (not just
// unit-tested in isolation). ---

func TestClient_GetSession_404_ReturnsTypedNotFoundError(t *testing.T) {
	// GetSession's 404 branch short-circuits via sessionNotFoundError before
	// ever reading resp.Body (it doesn't need the server's message), so this
	// case intentionally does NOT carry a raw Body — unlike AddToQueue's 409
	// below, which reads the body through errorFromResponse. Session ID is
	// still recoverable via Details.
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"Session not found"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.GetSession("missing")
	if err == nil {
		t.Fatal("GetSession returned nil error for a 404 response")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed to extract *APIError")
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", apiErr.Status, http.StatusNotFound)
	}
	if apiErr.Details["session_id"] != "missing" {
		t.Errorf("Details[session_id] = %v, want %q", apiErr.Details["session_id"], "missing")
	}
}

func TestClient_AddToQueue_409_ReturnsTypedConflictError(t *testing.T) {
	const wantBody = `{"error":{"code":"queue_full","message":"Queue is full. Maximum 5 messages allowed."}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/full-session/queue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(wantBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.AddToQueue("full-session", "one more message")
	if err == nil {
		t.Fatal("AddToQueue returned nil error for a 409 response")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is(err, ErrConflict) = false, want true; err = %v", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed to extract *APIError")
	}
	if apiErr.Code != "queue_full" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "queue_full")
	}
	if apiErr.Message != "Queue is full. Maximum 5 messages allowed." {
		t.Errorf("Message = %q, want the server-supplied message preserved verbatim", apiErr.Message)
	}
	if string(apiErr.Body) != wantBody {
		t.Errorf("Body = %q, want %q (raw response body must be preserved)", apiErr.Body, wantBody)
	}
}

// TestClient_DeleteLoop_ReturnsNilOnSuccessStatuses pins DeleteLoop's
// documented "nil on both 200 and 204" contract (mitto-7gta.25), matching
// the other loop/queue mutators (ClearQueue, DeleteSession) in this file.
func TestClient_DeleteLoop_ReturnsNilOnSuccessStatuses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			var gotMethod, gotPath string
			mux := http.NewServeMux()
			mux.HandleFunc("/mitto/api/sessions/sess-1/loop", func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				w.WriteHeader(status)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			c := New(ts.URL)
			if err := c.DeleteLoop("sess-1"); err != nil {
				t.Fatalf("DeleteLoop: %v, want nil for status %d", err, status)
			}
			if gotMethod != http.MethodDelete {
				t.Errorf("server saw method %q, want DELETE", gotMethod)
			}
			if gotPath != "/mitto/api/sessions/sess-1/loop" {
				t.Errorf("server saw path %q, want /mitto/api/sessions/sess-1/loop", gotPath)
			}
		})
	}
}

// TestClient_DeleteLoop_404_ReturnsTypedNotFoundError mirrors GetLoop's 404
// handling: DeleteLoop must surface a typed *APIError satisfying
// errors.Is(err, ErrNotFound), with the session ID recoverable via Details,
// rather than falling through to the generic apiError path.
func TestClient_DeleteLoop_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/no-loop/loop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	err := c.DeleteLoop("no-loop")
	if err == nil {
		t.Fatal("DeleteLoop returned nil error for a 404 response")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed to extract *APIError")
	}
	if apiErr.Details["session_id"] != "no-loop" {
		t.Errorf("Details[session_id] = %v, want %q", apiErr.Details["session_id"], "no-loop")
	}
}

// TestClient_DeleteLoop_500_ReturnsGenericAPIError pins the fallback branch:
// any non-{200,204,404} status goes through the generic c.apiError path, so
// the error still satisfies errors.Is against the canonical 5xx sentinel.
func TestClient_DeleteLoop_500_ReturnsGenericAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/loop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"boom"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	err := c.DeleteLoop("sess-1")
	if err == nil {
		t.Fatal("DeleteLoop returned nil error for a 500 response")
	}
	if !errors.Is(err, ErrServerError) {
		t.Errorf("errors.Is(err, ErrServerError) = false, want true; err = %v", err)
	}
}
