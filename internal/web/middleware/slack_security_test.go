package middleware

import (
	"net/http"
	"testing"
)

func TestSlackCatalogPathsRequireAuthenticationAndCSRF(t *testing.T) {
	auth := NewAuthManager(nil)
	auth.apiPrefix = "/mitto"
	csrf := NewCSRFManager()
	csrf.apiPrefix = "/mitto"

	for _, path := range []string{
		"/mitto/api/slack/apps",
		"/mitto/api/slack/apps/app-id/installations",
		"/mitto/api/slack/installations/installation-id/channels",
	} {
		if auth.isPublicPath(path) {
			t.Errorf("Slack path %q is unexpectedly public", path)
		}
	}

	for _, path := range []string{
		"/mitto/api/slack/apps",
		"/mitto/api/slack/apps/app-id/token",
		"/mitto/api/slack/installations/installation-id",
	} {
		if csrf.isCSRFExemptPath(path) {
			t.Errorf("Slack mutation path %q is unexpectedly CSRF-exempt", path)
		}
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !isStateChangingMethod(method) {
			t.Errorf("method %s must require CSRF validation", method)
		}
	}
}
