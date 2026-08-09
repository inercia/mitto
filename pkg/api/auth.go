package client

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
)

// authProvider decorates outgoing requests and WebSocket handshakes with
// credentials. The zero value of Client uses noAuth, which is a no-op, so
// New(baseURL) keeps its historical zero-config, unauthenticated behaviour
// (mitto-rwxq.4 plan decision — this is the primary compatibility
// constraint for the 226 existing test consumers).
type authProvider interface {
	// applyREST mutates an outgoing HTTP request to attach credentials.
	// It must never place a token in the URL or query string.
	applyREST(req *http.Request) error

	// applyWS decorates the WebSocket upgrade handshake headers.
	// It must never place a token in the URL or query string.
	applyWS(h http.Header) error
}

// noAuth is the default, zero-config authentication mode: it adds nothing.
type noAuth struct{}

func (noAuth) applyREST(*http.Request) error { return nil }
func (noAuth) applyWS(http.Header) error     { return nil }

// bearerAuth authenticates via "Authorization: Bearer <token>", matching the
// backend's shared-token support (mitto-7gta.26). The token is sourced from
// supplier on every request so callers can rotate it (env var, keychain,
// config file) without reconstructing the Client. The token is never logged
// and never placed in a URL or query string.
type bearerAuth struct {
	supplier func() (string, error)
}

func (b bearerAuth) applyREST(req *http.Request) error {
	tok, err := b.supplier()
	if err != nil {
		return fmt.Errorf("bearer auth: resolve token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (b bearerAuth) applyWS(h http.Header) error {
	tok, err := b.supplier()
	if err != nil {
		return fmt.Errorf("bearer auth: resolve token: %w", err)
	}
	h.Set("Authorization", "Bearer "+tok)
	return nil
}

// cookieAuth authenticates via the interactive login flow: a mitto_session
// cookie (held in the Client's cookie jar) plus CSRF parity for
// state-changing requests, for parity with the browser. Cookies themselves
// are attached to REST requests automatically by the jar via http.Client;
// applyREST only adds the CSRF header. The WebSocket handshake does not go
// through http.Client, but Connect passes the same jar to websocket.Dialer,
// which attaches jar cookies to the handshake itself — so applyWS has
// nothing to add (WebSocket upgrades are also CSRF-exempt, matching
// internal/web/middleware/csrf.go).
type cookieAuth struct {
	client *Client
}

func (c cookieAuth) applyREST(req *http.Request) error {
	if isStateChangingMethodClient(req.Method) {
		req.Header.Set(csrfTokenHeaderClient, c.client.csrfToken)
	}
	return nil
}

func (c cookieAuth) applyWS(http.Header) error { return nil }

// csrfTokenHeaderClient mirrors internal/web/middleware.csrfTokenHeader so
// the SDK does not import the web package (kept as a local literal since
// this is a stable, documented header name — see docs/devel/rest-api-conventions.md).
const csrfTokenHeaderClient = "X-CSRF-Token"

// isStateChangingMethodClient mirrors isStateChangingMethod in
// internal/web/middleware/csrf.go: only these methods require the CSRF
// header under the double-submit cookie pattern.
func isStateChangingMethodClient(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// ensureJar lazily installs a cookiejar.Jar on the Client's http.Client if
// one is not already present. Called on first Login so New(baseURL) without
// Login never allocates a jar (keeps the no-auth default pristine).
func (c *Client) ensureJar() error {
	if c.httpClient.Jar != nil {
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("create cookie jar: %w", err)
	}
	c.httpClient.Jar = jar
	return nil
}
