package web

import (
	"bufio"
	"net"
	"net/http"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/defense"
)

// shouldEnableScannerDefense determines whether scanner defense should be enabled.
// It is enabled by default when external access is configured (ExternalPort >= 0),
// unless explicitly disabled in config.
func shouldEnableScannerDefense(webConfig *configPkg.WebConfig) bool {
	if webConfig == nil {
		return false
	}

	// Check if explicitly configured
	if webConfig.Security != nil && webConfig.Security.ScannerDefense != nil {
		return webConfig.Security.ScannerDefense.Enabled
	}

	// Enable by default when external access is configured
	// External port >= 0 means external access is enabled (0 = random, >0 = specific port)
	return webConfig.ExternalPort >= 0
}

// getScannerDefenseConfig returns the scanner defense config from WebSecurity.
func getScannerDefenseConfig(webConfig *configPkg.WebConfig) *configPkg.ScannerDefenseConfig {
	if webConfig == nil || webConfig.Security == nil {
		return nil
	}
	return webConfig.Security.ScannerDefense
}

// configToDefenseConfig converts ScannerDefenseConfig to defense.Config.
// If cfg is nil, returns defaults with Enabled set based on externalAccessEnabled.
func configToDefenseConfig(cfg *configPkg.ScannerDefenseConfig, enabled bool) defense.Config {
	c := defense.DefaultConfig()
	c.Enabled = enabled

	// Set persistence path
	if path, err := appdir.DefenseBlocklistPath(); err == nil {
		c.PersistPath = path
	}

	if cfg == nil {
		return c
	}

	// Apply explicit configuration values
	if cfg.RateLimit > 0 {
		c.RateLimit = cfg.RateLimit
	}
	if cfg.RateWindowSeconds > 0 {
		c.RateWindow = time.Duration(cfg.RateWindowSeconds) * time.Second
	}
	if cfg.ErrorRateThreshold > 0 {
		c.ErrorRateThreshold = cfg.ErrorRateThreshold
	}
	if cfg.MinRequestsForAnalysis > 0 {
		c.MinRequestsForAnalysis = cfg.MinRequestsForAnalysis
	}
	if cfg.SuspiciousPathThreshold > 0 {
		c.SuspiciousPathThreshold = cfg.SuspiciousPathThreshold
	}
	if cfg.BlockDurationSeconds > 0 {
		c.BlockDuration = time.Duration(cfg.BlockDurationSeconds) * time.Second
	}
	if len(cfg.Whitelist) > 0 {
		c.Whitelist = cfg.Whitelist
	}

	return c
}

// defenseRecordingMiddleware records requests for analysis by the scanner defense system.
// Only records requests from external connections (not localhost).
func (s *Server) defenseRecordingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.defense == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Only record requests from external connections
		isExternal, _ := r.Context().Value(ContextKeyExternalConnection).(bool)
		if !isExternal {
			next.ServeHTTP(w, r)
			return
		}

		ip := getClientIPWithProxyCheck(r)

		// Wrap response writer to capture status code
		wrapped := &defenseStatusRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(wrapped, r)

		// Record request for analysis (async to not block response)
		go s.defense.RecordRequest(ip, &defense.RequestInfo{
			Path:       r.URL.Path,
			Method:     r.Method,
			StatusCode: wrapped.statusCode,
			UserAgent:  r.UserAgent(),
			Timestamp:  time.Now(),
		})
	})
}

// defenseStatusRecorder wraps http.ResponseWriter to capture the status code.
type defenseStatusRecorder struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (r *defenseStatusRecorder) WriteHeader(code int) {
	if !r.headerWritten {
		r.statusCode = code
		r.headerWritten = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *defenseStatusRecorder) Write(b []byte) (int, error) {
	if !r.headerWritten {
		r.statusCode = http.StatusOK
		r.headerWritten = true
	}
	return r.ResponseWriter.Write(b)
}

// Hijack implements http.Hijacker for WebSocket support.
func (r *defenseStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Flush implements http.Flusher for streaming support.
func (r *defenseStatusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter.
func (r *defenseStatusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
