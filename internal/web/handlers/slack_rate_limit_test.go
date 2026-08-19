package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/slackcatalog"
)

func TestWriteSlackErrorClassifiesRateLimitWithoutProviderDetails(t *testing.T) {
	response := httptest.NewRecorder()
	writeSlackError(response, slackcatalog.ErrRateLimited)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.Code)
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want safe fallback", response.Header().Get("Retry-After"))
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"rate_limited"`) || strings.Contains(body, slackcatalog.ErrRateLimited.Error()) {
		t.Fatalf("body = %s, want canonical value-free rate-limit envelope", body)
	}
}
