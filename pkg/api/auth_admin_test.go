package api

import (
	"errors"
	"net/http"
	"testing"
)

// GetHealth is the last RouteCoverage-listed method without any test
// (mitto-rwxq.9 Testing phase). It is deliberately excluded from
// negative_paths_test.go's shared matrix because it decodes the response
// body unconditionally before checking the status code (auth_admin.go), so
// its error contract differs from every other resource method: on a non-2xx
// status it still returns a non-nil *HealthStatus alongside the error, and
// the returned *APIError does not carry Body (the body was already consumed
// by the decode step). These tests characterize that documented asymmetry
// directly rather than forcing it into the standard matrix.

func TestClient_GetHealth_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/health").
		RespondJSON(http.StatusOK, `{"status":"ok"}`)

	got, err := f.Client().GetHealth()
	if err != nil {
		t.Fatalf("GetHealth: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("HealthStatus.Status = %q, want %q", got.Status, "ok")
	}
}

func TestClient_GetHealth_ErrorStatus_ReturnsTypedAPIErrorAndStatus(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/health").
		RespondJSON(http.StatusServiceUnavailable, `{"status":"degraded"}`)

	got, err := f.Client().GetHealth()
	// Unlike every other resource method, GetHealth returns a non-nil result
	// alongside the error: the body decodes fine before the status check.
	if got == nil || got.Status != "degraded" {
		t.Errorf("HealthStatus = %+v, want Status=degraded even on error", got)
	}
	assertAPIError(t, err, ErrUnavailable, http.StatusServiceUnavailable, CodeUnavailable)
}

func TestClient_GetHealth_MalformedBody_ReturnsDecodeErrorNotAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/health").RespondMalformed(http.StatusOK)

	_, err := f.Client().GetHealth()
	if err == nil {
		t.Fatal("GetHealth: got nil error for a non-JSON body")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("GetHealth returned an *APIError (%+v) for a decode failure; want a plain wrapped error", apiErr)
	}
}
