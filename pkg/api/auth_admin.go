package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// HealthStatus is the response from GET /api/health, the server's liveness
// probe. Only the field every caller can rely on is typed; additional
// server-reported metrics (sessions/stored_sessions) are not modeled since
// no current SDK consumer needs them.
type HealthStatus struct {
	Status string `json:"status"`
}

// GetHealth calls GET /api/health. It is intentionally NOT behind
// authentication (docs/devel/web-interface.md), so a nil error here proves
// reachability only — it does not validate a configured credential.
func (c *Client) GetHealth() (*HealthStatus, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/health"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get health: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get health: %w", err)
	}
	defer resp.Body.Close()

	var status HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("get health: decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &status, &APIError{Op: "get health", Status: resp.StatusCode, Code: errorCodeForStatus(resp.StatusCode), Message: status.Status}
	}
	return &status, nil
}

// AuthInfoResponse is the response from GET /api/auth-info: which auth
// method(s) the server has configured.
type AuthInfoResponse struct {
	Simple     bool `json:"simple"`
	Cloudflare bool `json:"cloudflare"`
}

// GetAuthInfo calls GET /api/auth-info, a public endpoint (no credential
// required) used by the login page to adapt its UI.
func (c *Client) GetAuthInfo() (*AuthInfoResponse, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/auth-info"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get auth info: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get auth info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get auth info", resp)
	}
	var info AuthInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("get auth info: decode: %w", err)
	}
	return &info, nil
}

// RotateTokenResponse is the response from POST /api/auth/rotate-token.
type RotateTokenResponse struct {
	// Fingerprint is the new token's short, non-secret SHA-256-derived
	// identifier. The token value itself is never returned here — see
	// docs/config/web/README.md.
	Fingerprint string `json:"fingerprint"`
}

// RotateSharedToken calls POST /api/auth/rotate-token (mitto-pscc.9):
// localhost-only, server-side rotation of the shared bearer token. On
// success, every other client holding the previous token is rejected
// immediately and must re-read instance.json. Returns an *APIError (via
// errors.As) wrapping 403 if called against a non-loopback listener, or 409
// if no shared token is configured or the configured one is
// operator-managed (env/settings/keychain) rather than adopted from
// instance.json, since rotating an operator secret this way is out of scope.
func (c *Client) RotateSharedToken() (*RotateTokenResponse, error) {
	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/auth/rotate-token"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("rotate shared token: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("rotate shared token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("rotate shared token", resp)
	}
	var result RotateTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("rotate shared token: decode: %w", err)
	}
	return &result, nil
}
