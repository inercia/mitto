package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/slackcatalog"
)

type handlerCredentials struct {
	values map[secrets.CredentialRef]string
}

func (c *handlerCredentials) Put(ref secrets.CredentialRef, value string) error {
	c.values[ref] = value
	return nil
}
func (c *handlerCredentials) Resolve(ref secrets.CredentialRef) (string, error) {
	value, ok := c.values[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}
func (c *handlerCredentials) Status(ref secrets.CredentialRef) (secrets.CredentialStatus, error) {
	_, ok := c.values[ref]
	return secrets.CredentialStatus{Configured: ok}, nil
}
func (c *handlerCredentials) Delete(ref secrets.CredentialRef) error {
	if _, ok := c.values[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(c.values, ref)
	return nil
}

type handlerSlack struct{}

func (handlerSlack) ValidateApp(_ context.Context, token string) (string, error) {
	if token != "write-only-token" {
		return "", slackcatalog.ErrUnavailable
	}
	return "A123", nil
}
func (handlerSlack) ValidateInstallation(context.Context, string) (slackcatalog.InstallationIdentity, error) {
	return slackcatalog.InstallationIdentity{}, slackcatalog.ErrUnavailable
}
func (handlerSlack) ListPublicChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

func newSlackHandlers(t *testing.T) *Handlers {
	t.Helper()
	service := slackcatalog.NewService(
		slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		&handlerCredentials{values: make(map[secrets.CredentialRef]string)},
		handlerSlack{}, nil,
	)
	return New(Deps{SlackCatalog: service})
}

func TestSlackHandlersCreateListAndNeverReturnToken(t *testing.T) {
	h := newSlackHandlers(t)
	request := httptest.NewRequest(http.MethodPost, "/api/slack/apps", strings.NewReader(
		`{"name":"Production","app_token":"write-only-token"}`,
	))
	response := httptest.NewRecorder()
	h.HandleSlackAppCreate(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "write-only-token") || !strings.Contains(response.Body.String(), `"token_configured":true`) {
		t.Fatalf("unsafe create response: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	h.HandleSlackAppsList(response, httptest.NewRequest(http.MethodGet, "/api/slack/apps", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "write-only-token") {
		t.Fatalf("unsafe list response: status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"slack_app_id":"A123"`) {
		t.Fatalf("list response missing derived identity: %s", response.Body.String())
	}
}

func TestSlackHandlersCanonicalErrors(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		h := New(Deps{})
		response := httptest.NewRecorder()
		h.HandleSlackAppsList(response, httptest.NewRequest(http.MethodGet, "/api/slack/apps", nil))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
			t.Fatalf("status=%d retry=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"code":"unavailable"`) {
			t.Fatalf("body=%s", response.Body.String())
		}
	})

	t.Run("bad request", func(t *testing.T) {
		h := newSlackHandlers(t)
		response := httptest.NewRecorder()
		h.HandleSlackAppCreate(response, httptest.NewRequest(http.MethodPost, "/api/slack/apps", strings.NewReader(`{"name":`)))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("conflict", func(t *testing.T) {
		h := newSlackHandlers(t)
		body := `{"name":"One","app_token":"write-only-token"}`
		first := httptest.NewRecorder()
		h.HandleSlackAppCreate(first, httptest.NewRequest(http.MethodPost, "/api/slack/apps", strings.NewReader(body)))
		second := httptest.NewRecorder()
		h.HandleSlackAppCreate(second, httptest.NewRequest(http.MethodPost, "/api/slack/apps", strings.NewReader(body)))
		if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), `"code":"conflict"`) {
			t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
		}
	})

	t.Run("channel limit", func(t *testing.T) {
		h := newSlackHandlers(t)
		request := httptest.NewRequest(http.MethodGet, "/api/slack/installations/id/channels?limit=bad", nil)
		response := httptest.NewRecorder()
		h.HandleSlackInstallationChannels(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestWriteSlackErrorMapsReferencesToConflict(t *testing.T) {
	response := httptest.NewRecorder()
	writeSlackError(response, errors.Join(slackcatalog.ErrReferenced, errors.New("in use")))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
