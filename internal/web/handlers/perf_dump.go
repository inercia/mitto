package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/inercia/mitto/internal/web/middleware"
)

const (
	// maxPerfDumpBodySize caps the JSONL body accepted by HandlePerfDump. The
	// client-side perf buffer is itself capped at 4096 entries
	// (web/static/utils/perfMarks.js's PERF_BUFFER_CAP), so this is generous
	// headroom rather than a tight budget.
	maxPerfDumpBodySize = 5 * 1024 * 1024 // 5MB

	// perfDumpResultsDirEnv overrides the results root for uncommon layouts
	// (e.g. a dev setup where the process cwd isn't the repo root).
	perfDumpResultsDirEnv = "MITTO_PERF_RESULTS_DIR"

	// perfDumpDefaultResultsDir is the default results root, relative to the
	// process's working directory. The manual playbook always launches
	// Mitto from the repo root — see docs/devel/ui-responsiveness-benchmarks.md.
	perfDumpDefaultResultsDir = "tests/ui/perf/results"
)

// perfDumpLabelPattern whitelists the `label` query param: lowercase
// alphanumerics and hyphens only, starting with an alphanumeric. Rejects
// path traversal ("..", "/", "\\"), spaces, and the empty string.
var perfDumpLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// PerfDumpResponse is the response body for a successful POST /api/perf/dump.
type PerfDumpResponse struct {
	OK    bool   `json:"ok"`
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

// HandlePerfDump handles POST /api/perf/dump — a reusable perf-sample
// delivery endpoint for browser legs that have no native file-write bind
// (e.g. iOS Simulator Safari, which has no window.mittoDumpPerfBuffer).
// Accepts the same per-line JSON shape scripts/perf-summary.mjs's
// aggregateResults() already consumes for the WKWebView leg (mitto-sus.2)
// and appends the raw body to
// tests/ui/perf/results/latest-<label>/samples.jsonl.
//
// SECURITY: dev/debug-only. Only present on the route table at all when
// MITTO_PERF_DUMP=1 (see routes.go) — absent entirely otherwise, so shipping
// builds return 404 rather than 403 (no surface to probe). Even then,
// restricted to loopback connections only, mirroring image_frompath.go's
// defense-in-depth: external-listener requests are rejected regardless of
// client IP (context-level check, immune to a spoofed X-Forwarded-For),
// then a second IP-based loopback check as a redundant backstop. Internal
// (loopback) connections are also exempt from CSRF (see
// middleware.CSRFManager.CSRFMiddleware), so no CSRF token is required here.
func (h *Handlers) HandlePerfDump(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): reject ALL requests from the
	// external listener, even if X-Forwarded-For is spoofed to localhost.
	if middleware.IsExternalConnection(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected perf-dump request from external listener",
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "This endpoint is only available from localhost")
		return
	}

	// Security check 2: redundant IP-based loopback check.
	clientIP := middleware.GetClientIPWithProxyCheck(r)
	if !middleware.IsLoopbackIP(clientIP) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected perf-dump request from non-localhost",
				"client_ip", clientIP,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "This endpoint is only available from localhost")
		return
	}

	label := r.URL.Query().Get("label")
	if !perfDumpLabelPattern.MatchString(label) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid or missing 'label' query parameter")
		return
	}
	// scenario is informational only (logged, never used to build a path) —
	// no validation needed beyond the body size cap below.
	scenario := r.URL.Query().Get("scenario")

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPerfDumpBodySize+1))
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Failed to read request body")
		return
	}
	if len(body) > maxPerfDumpBodySize {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "", "Request body too large")
		return
	}

	resultsRoot := os.Getenv(perfDumpResultsDirEnv)
	if resultsRoot == "" {
		resultsRoot = perfDumpDefaultResultsDir
	}
	legDir := filepath.Join(resultsRoot, "latest-"+label)
	if err := os.MkdirAll(legDir, 0o755); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to create perf-dump results directory", "dir", legDir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to create results directory")
		return
	}

	samplesPath := filepath.Join(legDir, "samples.jsonl")
	f, err := os.OpenFile(samplesPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to open perf-dump samples file", "path", samplesPath, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to open results file")
		return
	}
	defer f.Close()

	n, err := f.Write(body)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to write perf-dump samples", "path", samplesPath, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to write results file")
		return
	}

	absPath, err := filepath.Abs(samplesPath)
	if err != nil {
		absPath = samplesPath
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Perf-dump sample written",
			"label", label,
			"scenario", scenario,
			"path", absPath,
			"bytes", n,
		)
	}

	writeJSONOK(w, PerfDumpResponse{OK: true, Path: absPath, Bytes: n})
}
