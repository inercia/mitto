package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// negCase describes one resource call exercised by the negative-path matrix
// (mitto-rwxq.9): a route to register on the fixture, and the client call
// that should hit exactly that route. call receives a *Client already
// pointed at the fixture (or an unreachable/hanging setup, depending on the
// sub-test) and returns the error observed.
type negCase struct {
	name   string
	method string
	path   string
	call   func(c *Client) error
}

// negCases enumerates one representative call per already-covered resource
// group. This is deliberately a representative sample, not exhaustive over
// every *Client method: the goal is proving the negative-path shapes
// (envelopes, malformed bodies, network failure, cancellation) are handled
// uniformly, which per-resource happy-path tests already pin per method.
var negCases = []negCase{
	{"GetSession", http.MethodGet, "/mitto/api/sessions/sess-1", func(c *Client) error {
		_, err := c.GetSession("sess-1")
		return err
	}},
	{"ListQueue", http.MethodGet, "/mitto/api/sessions/sess-1/queue", func(c *Client) error {
		_, err := c.ListQueue("sess-1")
		return err
	}},
	{"AddToQueue", http.MethodPost, "/mitto/api/sessions/sess-1/queue", func(c *Client) error {
		_, err := c.AddToQueue("sess-1", "hi")
		return err
	}},
	{"GetLoop", http.MethodGet, "/mitto/api/sessions/sess-1/loop", func(c *Client) error {
		_, err := c.GetLoop("sess-1")
		return err
	}},
	{"ListImages", http.MethodGet, "/mitto/api/sessions/sess-1/images", func(c *Client) error {
		_, err := c.ListImages("sess-1")
		return err
	}},
	{"GetSessionSettings", http.MethodGet, "/mitto/api/sessions/sess-1/settings", func(c *Client) error {
		_, err := c.GetSessionSettings("sess-1")
		return err
	}},
	// GetHealth is deliberately excluded: it decodes the body unconditionally
	// before checking the status code (auth_admin.go), so it doesn't fit this
	// matrix's "typed *APIError on non-2xx" assumption; that asymmetry is its
	// own documented contract, not a defect.
	{"GetAuthInfo", http.MethodGet, "/mitto/api/auth-info", func(c *Client) error {
		_, err := c.GetAuthInfo()
		return err
	}},
	{"GetPromptArgCache", http.MethodGet, "/mitto/api/sessions/sess-1/prompt-arg-cache", func(c *Client) error {
		_, err := c.GetPromptArgCache("sess-1", "p")
		return err
	}},
	{"GetSessionChanges", http.MethodGet, "/mitto/api/sessions/sess-1/changes", func(c *Client) error {
		_, err := c.GetSessionChanges("sess-1")
		return err
	}},
	{"GetSessionUserData", http.MethodGet, "/mitto/api/sessions/sess-1/user-data", func(c *Client) error {
		_, err := c.GetSessionUserData("sess-1")
		return err
	}},
	{"ListRunningSessions", http.MethodGet, "/mitto/api/sessions/running", func(c *Client) error {
		_, err := c.ListRunningSessions()
		return err
	}},
	{"SetLoop", http.MethodPut, "/mitto/api/sessions/sess-1/loop", func(c *Client) error {
		_, err := c.SetLoop("sess-1", SetLoopRequest{Prompt: "p"})
		return err
	}},
	{"UpdateSessionSettings", http.MethodPatch, "/mitto/api/sessions/sess-1/settings", func(c *Client) error {
		_, err := c.UpdateSessionSettings("sess-1", map[string]bool{"beta_feature": true})
		return err
	}},
	{"UploadImage", http.MethodPost, "/mitto/api/sessions/sess-1/images", func(c *Client) error {
		_, err := c.UploadImage("sess-1", "a.png", "image/png", []byte("PNGDATA"))
		return err
	}},
	{"UploadFile", http.MethodPost, "/mitto/api/sessions/sess-1/files", func(c *Client) error {
		_, err := c.UploadFile("sess-1", "notes.txt", "text/plain", []byte("hello"))
		return err
	}},
}

// TestNegativePaths_CanonicalEnvelope pins that every representative call
// surfaces a typed *APIError from the canonical nested envelope
// ({"error":{"code":...,"message":...}}) with Status/Code/Body preserved.
func TestNegativePaths_CanonicalEnvelope(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.On(tc.method, tc.path).Fail(http.StatusInternalServerError, CodeServerError, "boom", map[string]any{"x": 1})

			err := tc.call(f.Client())
			apiErr := assertAPIError(t, err, ErrServerError, http.StatusInternalServerError, CodeServerError)
			if len(apiErr.Body) == 0 {
				t.Error("Body not preserved on the canonical envelope path")
			}
		})
	}
}

// TestNegativePaths_LegacyFlatEnvelope mirrors the above for the legacy flat
// shape ({"error":"code","message":"..."}), used by external-stable
// endpoints (docs/devel/rest-api-conventions.md §4).
func TestNegativePaths_LegacyFlatEnvelope(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.On(tc.method, tc.path).FailLegacy(http.StatusServiceUnavailable, CodeUnavailable, "down for maintenance")

			err := tc.call(f.Client())
			assertAPIError(t, err, ErrUnavailable, http.StatusServiceUnavailable, CodeUnavailable)
		})
	}
}

// TestNegativePaths_EmptyBody pins that a non-2xx with no body at all still
// falls back to a status-derived code and a generic message, per
// errorFromResponse (already unit-tested in isolation in errors_test.go);
// this proves the fallback is actually reachable through real call sites.
func TestNegativePaths_EmptyBody(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.On(tc.method, tc.path).RespondRaw(http.StatusBadGateway, "", nil)

			err := tc.call(f.Client())
			assertAPIError(t, err, nil, http.StatusBadGateway, CodeServerError)
		})
	}
}

// TestNegativePaths_NonJSONBody mirrors TestNegativePaths_EmptyBody for a
// non-empty, non-JSON error body.
func TestNegativePaths_NonJSONBody(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.On(tc.method, tc.path).RespondRaw(http.StatusInternalServerError, "text/plain", []byte("internal server error, sorry"))

			err := tc.call(f.Client())
			assertAPIError(t, err, ErrServerError, http.StatusInternalServerError, CodeServerError)
		})
	}
}

// TestNegativePaths_MalformedSuccessBody pins the "decode:" wrap path: a 2xx
// response whose body isn't valid JSON must surface a plain (non-*APIError)
// error mentioning "decode", not be silently swallowed or misparsed as a
// zero-value success.
func TestNegativePaths_MalformedSuccessBody(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.On(tc.method, tc.path).RespondMalformed(http.StatusOK)

			err := tc.call(f.Client())
			if err == nil {
				t.Fatal("got nil error for a malformed 2xx body")
			}
			if !strings.Contains(err.Error(), "decode") {
				t.Errorf("error = %q, want it to mention decode", err.Error())
			}
		})
	}
}

// TestNegativePaths_ConnectionRefused pins that a client pointed at nothing
// listening surfaces a plain network error (not an *APIError, since there is
// no HTTP response to parse) for every representative call.
func TestNegativePaths_ConnectionRefused(t *testing.T) {
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(newUnreachableClient(t))
			if err == nil {
				t.Fatal("got nil error against an unreachable server")
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				t.Errorf("got a typed *APIError (%v) for a network failure, want a plain wrapped error", apiErr)
			}
		})
	}
}

// TestNegativePaths_ContextCancellation_Login pins Login's ctx-awareness:
// a server that hangs on /api/csrf-token must be abandoned promptly once the
// caller's context is cancelled, surfacing a context-cancellation error
// rather than hanging for the fixture's lifetime.
func TestNegativePaths_ContextCancellation_Login(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/csrf-token").Hang()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := f.Client().Login(ctx, "alice", "hunter2")
	if err == nil {
		t.Fatal("got nil error for a cancelled Login")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("error = %q, want it to mention the context cancellation", err.Error())
	}
}
