package api

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeServer is a declarative httptest.Server fixture shared by pkg/api's
// resource tests (mitto-rwxq.9), replacing the historical pattern of each
// test hand-rolling an http.NewServeMux + httptest.NewServer + manual JSON
// literals. Routes are registered by exact "METHOD path" and are strict by
// default: a request to an unregistered route fails the test immediately
// instead of silently 404ing, so a method that builds the wrong URL is
// caught rather than passing for the wrong reason.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	routes   map[string]*fakeRoute
	requests []recordedRequest
}

// recordedRequest captures everything about a request the fixture received,
// so assertions can move off ad-hoc captured variables.
type recordedRequest struct {
	Method      string
	Path        string
	RawQuery    string
	Header      http.Header
	Body        []byte
	ContentType string
}

type fakeRoute struct {
	responses       []fakeResponse
	calls           int
	hangUntilCancel bool
}

type fakeResponse struct {
	status      int
	contentType string
	body        []byte
}

// newFakeServer starts the fixture and registers its teardown; callers never
// call Close themselves.
func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{routes: map[string]*fakeRoute{}, t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.handle)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// Client builds a *Client pointed at the fixture.
func (f *fakeServer) Client(opts ...Option) *Client {
	return New(f.srv.URL, opts...)
}

// URL returns the fixture's base URL, for tests that need to build a Client
// with additional setup beyond Client's options.
func (f *fakeServer) URL() string { return f.srv.URL }

// On registers (or re-selects) a route for further response configuration.
// path is matched literally against r.URL.Path -- callers substitute any
// {id}-style segments themselves, exactly as the real Client does when
// building request URLs.
func (f *fakeServer) On(method, path string) *routeBuilder {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method + " " + path
	route, ok := f.routes[key]
	if !ok {
		route = &fakeRoute{}
		f.routes[key] = route
	}
	return &routeBuilder{f: f, route: route}
}

// LastRequest returns the most recently received request, or nil if none.
func (f *fakeServer) LastRequest() *recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	last := f.requests[len(f.requests)-1]
	return &last
}

// Requests returns every request received so far, in order.
func (f *fakeServer) Requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		Header: r.Header.Clone(), Body: body, ContentType: r.Header.Get("Content-Type"),
	})
	route, ok := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()

	if !ok {
		f.t.Errorf("fakeServer: unregistered route %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
		return
	}

	f.mu.Lock()
	hang := route.hangUntilCancel
	f.mu.Unlock()
	if hang {
		// Block until the client gives up (context cancellation/timeout),
		// simulating a server that never responds.
		<-r.Context().Done()
		return
	}

	f.mu.Lock()
	resp := route.nextResponse()
	f.mu.Unlock()

	if resp.contentType != "" {
		w.Header().Set("Content-Type", resp.contentType)
	}
	w.WriteHeader(resp.status)
	if len(resp.body) > 0 {
		_, _ = w.Write(resp.body)
	}
}

// nextResponse returns the response for the next call, repeating the last
// registered response once the sequence is exhausted (or 200 with an empty
// body if none was ever registered). Must be called with f.mu held.
func (r *fakeRoute) nextResponse() fakeResponse {
	if len(r.responses) == 0 {
		return fakeResponse{status: http.StatusOK}
	}
	idx := r.calls
	if idx >= len(r.responses) {
		idx = len(r.responses) - 1
	}
	r.calls++
	return r.responses[idx]
}

// routeBuilder configures the response(s) for a single registered route.
// Chaining multiple Respond*/Fail* calls registers a sequence: each request
// consumes the next entry, and the last entry repeats thereafter -- useful
// for retry-shaped assertions (e.g. first call fails, second succeeds).
type routeBuilder struct {
	f     *fakeServer
	route *fakeRoute
}

// RespondRaw registers a raw response.
func (rb *routeBuilder) RespondRaw(status int, contentType string, body []byte) *routeBuilder {
	rb.f.mu.Lock()
	defer rb.f.mu.Unlock()
	rb.route.responses = append(rb.route.responses, fakeResponse{status: status, contentType: contentType, body: body})
	return rb
}

// RespondJSON registers a JSON response, setting Content-Type accordingly.
// body is a raw JSON literal, matching the style of the pre-existing tests.
func (rb *routeBuilder) RespondJSON(status int, body string) *routeBuilder {
	return rb.RespondRaw(status, "application/json", []byte(body))
}

// RespondMalformed registers a 2xx-with-non-JSON-body response, for
// exercising a resource method's "decode:" error-wrap path.
func (rb *routeBuilder) RespondMalformed(status int) *routeBuilder {
	return rb.RespondRaw(status, "application/json", []byte("not valid json"))
}

// Fail registers a canonical nested error envelope response
// ({"error":{"code":...,"message":...,"details":{...}}}), matching
// errorFromResponse's primary shape (docs/devel/rest-api-conventions.md §4).
// details may be nil.
func (rb *routeBuilder) Fail(status int, code, message string, details map[string]any) *routeBuilder {
	errObj := map[string]any{"code": code, "message": message}
	if details != nil {
		errObj["details"] = details
	}
	body, err := json.Marshal(map[string]any{"error": errObj})
	if err != nil {
		rb.f.t.Fatalf("fakeServer.Fail: marshal: %v", err)
	}
	return rb.RespondRaw(status, "application/json", body)
}

// FailLegacy registers the legacy flat error envelope
// ({"error":"code","message":"..."}), used by external-stable endpoints
// such as POST /api/callback/{token}.
func (rb *routeBuilder) FailLegacy(status int, code, message string) *routeBuilder {
	env := map[string]any{"error": code}
	if message != "" {
		env["message"] = message
	}
	body, err := json.Marshal(env)
	if err != nil {
		rb.f.t.Fatalf("fakeServer.FailLegacy: marshal: %v", err)
	}
	return rb.RespondRaw(status, "application/json", body)
}

// Hang makes the route block until the client's request context is done
// (cancelled or timed out), for exercising context-cancellation handling.
func (rb *routeBuilder) Hang() *routeBuilder {
	rb.f.mu.Lock()
	defer rb.f.mu.Unlock()
	rb.route.hangUntilCancel = true
	return rb
}

// newUnreachableClient returns a *Client pointed at an address nothing is
// listening on, for exercising the "connection refused" network-failure
// path without any server involved.
func newUnreachableClient(t *testing.T) *Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newUnreachableClient: listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("newUnreachableClient: close listener: %v", err)
	}
	return New("http://" + addr)
}

// assertAPIError asserts that err is a non-nil *APIError satisfying
// errors.Is(err, wantSentinel) (skipped when wantSentinel is nil) and having
// the given Status/Code (each skipped when zero/empty), returning the
// extracted *APIError for further field checks.
func assertAPIError(t *testing.T, err error, wantSentinel *APIError, wantStatus int, wantCode string) *APIError {
	t.Helper()
	if err == nil {
		t.Fatal("assertAPIError: got nil error")
	}
	if wantSentinel != nil && !errors.Is(err, wantSentinel) {
		t.Errorf("errors.Is(err, sentinel status=%d) = false, want true; err = %v", wantSentinel.Status, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As failed to extract *APIError from: %v", err)
	}
	if wantStatus != 0 && apiErr.Status != wantStatus {
		t.Errorf("Status = %d, want %d", apiErr.Status, wantStatus)
	}
	if wantCode != "" && apiErr.Code != wantCode {
		t.Errorf("Code = %q, want %q", apiErr.Code, wantCode)
	}
	return apiErr
}
