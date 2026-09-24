package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/web/middleware"
)

// perfDumpRequest builds a POST /api/perf/dump request from loopback with the
// given query string and body.
func perfDumpRequest(query, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/perf/dump?"+query, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	return req
}

// TestHandlePerfDump_ExternalConnectionRejected covers the primary
// defense-in-depth check: any request tagged as coming from the external
// listener is rejected regardless of RemoteAddr (mirrors
// image_frompath_test.go's spoofed-loopback case).
func TestHandlePerfDump_ExternalConnectionRejected(t *testing.T) {
	h := New(Deps{})

	req := perfDumpRequest("label=ios-safari", "{}\n")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.HandlePerfDump(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestHandlePerfDump_NonLoopbackRejected covers the redundant IP-based
// backstop check for a request that isn't marked external but whose
// RemoteAddr isn't loopback either.
func TestHandlePerfDump_NonLoopbackRejected(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/perf/dump?label=ios-safari", strings.NewReader("{}\n"))
	req.RemoteAddr = "192.168.1.100:54321"
	w := httptest.NewRecorder()

	h.HandlePerfDump(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestHandlePerfDump_InvalidLabel covers the label whitelist: missing,
// empty, and path-traversal attempts must all be rejected as 400s before any
// file I/O happens.
func TestHandlePerfDump_InvalidLabel(t *testing.T) {
	h := New(Deps{})

	cases := []string{
		"", // missing label param entirely (query below has no label=)
		"label=",
		"label=..%2F..%2Fetc",
		"label=UPPER",
		"label=has%20space",
		"label=" + strings.Repeat("a", 65), // over the 64-char cap
	}

	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			req := perfDumpRequest(query, "{}\n")
			w := httptest.NewRecorder()

			h.HandlePerfDump(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("query=%q: status = %d, want %d (body=%s)", query, w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

// TestHandlePerfDump_BodyTooLarge covers the 5MB body cap.
func TestHandlePerfDump_BodyTooLarge(t *testing.T) {
	t.Setenv(perfDumpResultsDirEnv, t.TempDir())
	h := New(Deps{})

	oversized := strings.Repeat("x", maxPerfDumpBodySize+1)
	req := perfDumpRequest("label=ios-safari", oversized)
	w := httptest.NewRecorder()

	h.HandlePerfDump(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestHandlePerfDump_HappyPath_WritesAndAppends covers the success path:
// a valid loopback POST with a whitelisted label appends the raw body to
// <results-root>/latest-<label>/samples.jsonl and returns {ok, path, bytes},
// and a second POST appends rather than overwrites (mirrors the manual
// playbook's per-scenario dump calls in ui-responsiveness-benchmarks.md).
func TestHandlePerfDump_HappyPath_WritesAndAppends(t *testing.T) {
	resultsRoot := t.TempDir()
	t.Setenv(perfDumpResultsDirEnv, resultsRoot)
	h := New(Deps{})

	line1 := `{"scenario":"composer.keystroke","metric":"p50","value":12.3,"ts":1}` + "\n"
	req1 := perfDumpRequest("label=ios-safari&scenario=composer.keystroke", line1)
	w1 := httptest.NewRecorder()
	h.HandlePerfDump(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want %d (body=%s)", w1.Code, http.StatusOK, w1.Body.String())
	}

	var resp1 PerfDumpResponse
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp1.OK {
		t.Fatalf("resp.OK = false, want true")
	}
	if resp1.Bytes != len(line1) {
		t.Fatalf("resp.Bytes = %d, want %d", resp1.Bytes, len(line1))
	}

	samplesPath := filepath.Join(resultsRoot, "latest-ios-safari", "samples.jsonl")
	got, err := os.ReadFile(samplesPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", samplesPath, err)
	}
	if string(got) != line1 {
		t.Fatalf("file content after 1st write = %q, want %q", got, line1)
	}

	// Second call with a different scenario must append, not overwrite.
	line2 := `{"scenario":"issue.render","metric":"p50","value":45.6,"ts":2}` + "\n"
	req2 := perfDumpRequest("label=ios-safari&scenario=issue.render", line2)
	w2 := httptest.NewRecorder()
	h.HandlePerfDump(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d, want %d (body=%s)", w2.Code, http.StatusOK, w2.Body.String())
	}

	got, err = os.ReadFile(samplesPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) after append: %v", samplesPath, err)
	}
	want := line1 + line2
	if string(got) != want {
		t.Fatalf("file content after append = %q, want %q", got, want)
	}
}

// TestHandlePerfDump_DefaultResultsDir covers the fallback to
// perfDumpDefaultResultsDir when MITTO_PERF_RESULTS_DIR is unset — the
// manual playbook always launches Mitto from the repo root, so this is the
// path real operator runs take.
func TestHandlePerfDump_DefaultResultsDir(t *testing.T) {
	t.Setenv(perfDumpResultsDirEnv, "") // ensure unset for this test
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	h := New(Deps{})
	line := `{"scenario":"s","metric":"p50","value":1,"ts":1}` + "\n"
	req := perfDumpRequest("label=wkwebview", line)
	w := httptest.NewRecorder()

	h.HandlePerfDump(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", w.Code, http.StatusOK, w.Body.String())
	}

	samplesPath := filepath.Join(dir, perfDumpDefaultResultsDir, "latest-wkwebview", "samples.jsonl")
	got, err := os.ReadFile(samplesPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", samplesPath, err)
	}
	if string(got) != line {
		t.Fatalf("file content = %q, want %q", got, line)
	}
}
