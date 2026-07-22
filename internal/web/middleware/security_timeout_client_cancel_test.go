package middleware

// mitto-axsg: reproduction test for the "intermittent HTTP 503 with
// body_bytes=0 on legitimate GETs" bug (see bd show mitto-axsg).
//
// Root cause established in the investigate phase:
//   http.TimeoutHandler (Go 1.26.5 net/http/server.go:3853-3866) writes
//   StatusServiceUnavailable in TWO cases when ctx.Done() fires:
//     - context.DeadlineExceeded — genuine server-side timeout:
//         WriteHeader(503) + io.WriteString(w, h.errorBody())
//     - default (context.Canceled — CLIENT DISCONNECT, parent-ctx cancel):
//         WriteHeader(503) with NO body
//
// RequestTimeoutMiddleware wraps every non-WebSocket request in
// http.TimeoutHandler, so a client that disconnects mid-flight (WKWebView
// tearing down in-flight requests on navigation) causes the server-side
// ResponseWriter to receive a 503 with zero bytes written — which the
// access-log wrapper then records verbatim.
//
// The correct behavior is to NOT report a client cancellation as a 503
// server error (a "499 client closed request" or a distinguishable
// non-503 status is the industry-standard equivalent, or omit the write
// entirely since the client has already disconnected). The bug is that
// context.Canceled and context.DeadlineExceeded are conflated into one
// 503 status.
//
// This test exercises the server-side ResponseWriter directly (which
// is what the access-log wrapper sees) and cancels the request context
// mid-flight — exactly the sequence produced by a real TCP client
// disconnect on the http.Server side. It asserts the post-fix
// expectation and therefore FAILS on the current buggy code.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRequestTimeoutMiddleware_ClientCancel_DoesNotEmit503 reproduces the
// mitto-axsg symptom: on client disconnect (context.Canceled), the
// middleware writes 503 with body_bytes=0 to the outer ResponseWriter.
func TestRequestTimeoutMiddleware_ClientCancel_DoesNotEmit503(t *testing.T) {
	// Inner handler blocks until the request context is canceled — this
	// mirrors a slow handler (e.g. bd/aux-backed /api/issues, /api/config)
	// that the client abandons mid-flight.
	handlerEntered := make(chan struct{})
	handler := RequestTimeoutMiddleware(DefaultRequestTimeout)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		<-r.Context().Done()
		// Attempt a normal write after cancellation — http.TimeoutHandler
		// will have already committed to the outer ResponseWriter, so
		// these are effectively no-ops. Retained so the test exercises
		// the same handler shape as real request handlers.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("late"))
	}))

	// Cancelable request context — canceling this simulates a TCP client
	// disconnect. Go's http.Server cancels r.Context() on client
	// disconnect via the same mechanism (net/http closes cancelCtx when
	// the connection dies), so this is a faithful reproduction of the
	// server-visible sequence.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil).WithContext(ctx)

	// The outer ResponseWriter — this is what the access-log wrapper
	// sees. It records the status code and the bytes written.
	rec := httptest.NewRecorder()

	// Drive the middleware on a goroutine so we can cancel the request
	// context after the inner handler has been entered.
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for the inner handler to be running.
	select {
	case <-handlerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("inner handler was never entered")
	}

	// Simulate the client disconnect: cancel the request context.
	// http.TimeoutHandler.ServeHTTP will observe ctx.Done() with
	// ctx.Err() == context.Canceled and hit the "default" arm of the
	// switch — WriteHeader(503) with no body.
	cancel()

	// Wait for the middleware goroutine to finish writing.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware did not return after context cancellation")
	}

	status := rec.Code
	bodyBytes := rec.Body.Len()
	t.Logf("server-side response: status=%d body_bytes=%d", status, bodyBytes)

	// The bug symptom in access.log is exactly `503 body_bytes=0`.
	// Assert the post-fix expectation: this specific combination must
	// not occur on a client cancellation. Acceptable post-fix outcomes
	// include: a distinct status (e.g. 499), a non-empty body on 503
	// (so it is distinguishable from the client-cancel path), or the
	// middleware skipping the write entirely (statusCode left at the
	// wrapper default of 200 with 0 bytes — the access-log wrapper
	// treats <400 as a non-error).
	if status == http.StatusServiceUnavailable && bodyBytes == 0 {
		t.Fatalf("mitto-axsg: RequestTimeoutMiddleware emitted a bodyless 503 on client cancellation (status=%d, body_bytes=%d) — this is the exact access.log signature reported on the bead; expected the middleware to distinguish context.Canceled from context.DeadlineExceeded",
			status, bodyBytes)
	}
}
