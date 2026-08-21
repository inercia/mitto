package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
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

var slackCredentialRoutes = []struct {
	method string
	path   string
}{
	{http.MethodPost, "/api/slack/apps"},
	{http.MethodPut, "/api/slack/apps/app-id/token"},
	{http.MethodPost, "/api/slack/apps/app-id/installations"},
	{http.MethodPut, "/api/slack/installations/installation-id/token"},
}

func TestSlackCredentialRoutesRequireExternalAuthentication(t *testing.T) {
	auth := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer auth.Close()
	csrf := NewCSRFManager()
	defer csrf.Close()
	token, err := csrf.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}

	called := false
	handler := csrf.CSRFMiddleware(auth.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})))
	for _, route := range slackCredentialRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			called = false
			request := makeExternalRequest(httptest.NewRequest(route.method, route.path, strings.NewReader(`{"token":"canary"}`)))
			request.RemoteAddr = "203.0.113.10:12345"
			request.Header.Set(csrfTokenHeader, token)
			request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || called {
				t.Fatalf("status=%d handler_called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestSlackCredentialRoutesRequireValidCSRF(t *testing.T) {
	auth := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer auth.Close()
	session, err := auth.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	csrf := NewCSRFManager()
	defer csrf.Close()

	handler := csrf.CSRFMiddleware(auth.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	for _, route := range slackCredentialRoutes {
		for _, csrfCase := range []string{"missing", "mismatched"} {
			t.Run(route.method+" "+route.path+"/"+csrfCase, func(t *testing.T) {
				request := makeExternalRequest(httptest.NewRequest(route.method, route.path, strings.NewReader(`{"token":"canary"}`)))
				request.RemoteAddr = "203.0.113.11:12345"
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
				if csrfCase == "mismatched" {
					request.Header.Set(csrfTokenHeader, "header-token")
					request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookie-token"})
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusForbidden {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}
