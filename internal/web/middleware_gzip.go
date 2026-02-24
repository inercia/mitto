package web

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Gzip compression configuration
const (
	// Minimum response size to compress (smaller responses have compression overhead)
	gzipMinSize = 1024 // 1KB

	// Gzip compression level (1-9, where 6 is a good balance of speed/compression)
	gzipLevel = 6
)

// Content types that should be compressed
// Binary formats like images, videos, and already-compressed files are skipped
var gzipContentTypes = map[string]bool{
	"text/html":                true,
	"text/css":                 true,
	"text/plain":               true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/json":         true,
	"application/xml":          true,
	"application/xhtml+xml":    true,
	"image/svg+xml":            true,
	"application/octet-stream": false, // Binary, skip
	"image/png":                false,
	"image/jpeg":               false,
	"image/gif":                false,
	"image/webp":               false,
}

// gzipResponseWriter wraps http.ResponseWriter to provide gzip compression.
// It defers header writing until we know whether we're actually compressing,
// which depends on both content type and size.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzWriter       *gzip.Writer
	pool           *sync.Pool
	statusCode     int
	statusCaptured bool // True when WriteHeader was called (status captured but not sent)
	headersSent    bool // True when headers have been written to underlying writer
	shouldGzip     bool // True if content type is compressible
	compressing    bool // True if we're actually compressing (size >= minSize)
	minSize        int
	buffer         []byte // Buffer for checking minimum size
}

// WriteHeader captures the status code but defers actual header writing.
// Headers are sent when we know whether we're compressing (in Write or Close).
func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	if w.statusCaptured {
		return
	}
	w.statusCode = statusCode
	w.statusCaptured = true

	// Check if we should gzip based on content type
	contentType := w.Header().Get("Content-Type")
	w.shouldGzip = shouldGzipContentType(contentType)

	// Don't compress error responses or redirects
	if statusCode < 200 || statusCode >= 300 {
		w.shouldGzip = false
	}

	// Don't send headers yet - wait until we know the size
}

// sendHeaders sends the actual HTTP headers to the underlying writer.
// Called when we know whether we're compressing.
func (w *gzipResponseWriter) sendHeaders() {
	if w.headersSent {
		return
	}
	w.headersSent = true

	// Set gzip headers only if we're actually compressing
	if w.compressing {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.Header().Add("Vary", "Accept-Encoding")
	}

	statusCode := w.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write compresses the response body if appropriate.
func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.statusCaptured {
		// Detect content type if not set
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}

	// If content type isn't compressible, write directly
	if !w.shouldGzip {
		w.sendHeaders()
		return w.ResponseWriter.Write(b)
	}

	// Buffer data until we reach minimum size
	if !w.compressing {
		w.buffer = append(w.buffer, b...)
		if len(w.buffer) < w.minSize {
			// Still buffering, don't send anything yet
			return len(b), nil
		}
		// We have enough data, start gzip compression
		w.compressing = true
		w.sendHeaders()
		w.gzWriter = w.pool.Get().(*gzip.Writer)
		w.gzWriter.Reset(w.ResponseWriter)
		if _, err := w.gzWriter.Write(w.buffer); err != nil {
			return 0, err
		}
		w.buffer = nil
		return len(b), nil
	}

	return w.gzWriter.Write(b)
}

// Flush writes any buffered data and flushes the gzip writer.
func (w *gzipResponseWriter) Flush() {
	// If we buffered data but never reached minSize, write it uncompressed
	if len(w.buffer) > 0 && !w.compressing {
		// Not compressing - send headers without gzip, then write buffer
		w.sendHeaders()
		w.ResponseWriter.Write(w.buffer)
		w.buffer = nil
		return
	}

	if w.gzWriter != nil {
		w.gzWriter.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Close closes the gzip writer and returns it to the pool.
func (w *gzipResponseWriter) Close() error {
	// Flush any remaining buffered data
	w.Flush()

	// If no data was ever written, make sure headers are sent
	if !w.headersSent {
		w.sendHeaders()
	}

	if w.gzWriter != nil {
		err := w.gzWriter.Close()
		w.pool.Put(w.gzWriter)
		w.gzWriter = nil
		return err
	}
	return nil
}

// shouldGzipContentType returns true if the content type should be compressed.
func shouldGzipContentType(contentType string) bool {
	// Extract the base content type (without charset, etc.)
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	contentType = strings.TrimSpace(strings.ToLower(contentType))

	// Check explicit mappings
	if shouldGzip, ok := gzipContentTypes[contentType]; ok {
		return shouldGzip
	}

	// Default: compress text/* and application/*json, application/*xml
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	if strings.HasPrefix(contentType, "application/") {
		if strings.Contains(contentType, "json") || strings.Contains(contentType, "xml") {
			return true
		}
	}

	return false
}

// gzipWriterPool is a pool of gzip writers for reuse.
var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		w, _ := gzip.NewWriterLevel(io.Discard, gzipLevel)
		return w
	},
}

// gzipMiddleware returns middleware that compresses responses for external connections.
// It only compresses responses that:
// - Are for external connections (not localhost)
// - Have compressible content types (text/*, application/json, etc.)
// - Are larger than the minimum size threshold
// - Are not WebSocket upgrade requests
//
// This improves performance for Tailscale/external access while avoiding
// CPU overhead for local connections (native macOS app, localhost browser).
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip compression for non-external connections
		// Local connections don't benefit from compression (network is free)
		if !IsExternalConnection(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Skip if client doesn't accept gzip
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		// Skip WebSocket upgrade requests (they handle their own compression)
		if r.Header.Get("Upgrade") == "websocket" {
			next.ServeHTTP(w, r)
			return
		}

		// Wrap the response writer with gzip support
		gzw := &gzipResponseWriter{
			ResponseWriter: w,
			pool:           &gzipWriterPool,
			minSize:        gzipMinSize,
		}
		defer gzw.Close()

		next.ServeHTTP(gzw, r)
	})
}

// Hijack implements http.Hijacker for WebSocket support.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, io.ErrUnexpectedEOF
}

// Unwrap returns the underlying ResponseWriter.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
