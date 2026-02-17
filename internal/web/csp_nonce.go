package web

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	// nonceLength is the length of the CSP nonce in bytes (before base64 encoding).
	// 16 bytes = 128 bits of entropy, which is sufficient for CSP nonces.
	nonceLength = 16

	// noncePlaceholder is the placeholder in HTML files that gets replaced with the actual nonce.
	// This is used in script tags: <script nonce="{{CSP_NONCE}}">
	noncePlaceholder = "{{CSP_NONCE}}"

	// apiPrefixPlaceholder is the placeholder in HTML files that gets replaced with the API prefix.
	// This is used to inject the API prefix for frontend JavaScript.
	apiPrefixPlaceholder = "{{API_PREFIX}}"

	// isExternalPlaceholder is the placeholder in HTML files that gets replaced with the external connection status.
	// This is used to inject the external connection flag for frontend JavaScript.
	isExternalPlaceholder = "{{IS_EXTERNAL}}"
)

// generateCSPNonce generates a cryptographically secure random nonce for CSP.
func generateCSPNonce() (string, error) {
	b := make([]byte, nonceLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// cspNonceResponseWriter wraps http.ResponseWriter to inject CSP nonces and API prefix into HTML responses.
type cspNonceResponseWriter struct {
	http.ResponseWriter
	nonce               string
	apiPrefix           string
	isExternal          bool
	allowExternalImages bool
	config              SecurityConfig
	statusCode          int
	headerWritten       bool
	buffer              *bytes.Buffer
	isHTML              bool
}

// WriteHeader captures the status code and checks if this is an HTML response.
func (w *cspNonceResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode

	// Check if this is an HTML response
	contentType := w.Header().Get("Content-Type")
	w.isHTML = strings.Contains(contentType, "text/html")

	// For HTML responses, we need to buffer the content to inject nonces
	if w.isHTML {
		w.buffer = &bytes.Buffer{}
		// Don't write headers yet - we'll do it after processing the body
		return
	}

	// For non-HTML responses, set CSP header without nonce and write immediately
	w.setCSPHeader(false)
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write buffers HTML content or writes directly for non-HTML.
func (w *cspNonceResponseWriter) Write(b []byte) (int, error) {
	// If headers haven't been written, check content type from first write
	if !w.headerWritten && w.buffer == nil {
		// Check Content-Type header first, then detect from content
		contentType := w.Header().Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(b)
		}
		w.isHTML = strings.Contains(contentType, "text/html")

		if w.isHTML {
			w.buffer = &bytes.Buffer{}
		} else {
			w.setCSPHeader(false)
			w.headerWritten = true
			if w.statusCode == 0 {
				w.statusCode = http.StatusOK
			}
			w.ResponseWriter.WriteHeader(w.statusCode)
		}
	}

	if w.buffer != nil {
		return w.buffer.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush writes the buffered HTML content with nonces injected.
func (w *cspNonceResponseWriter) Flush() {
	if w.buffer == nil {
		return
	}

	// Replace placeholders in the HTML
	html := w.buffer.String()
	html = strings.ReplaceAll(html, noncePlaceholder, w.nonce)
	html = strings.ReplaceAll(html, apiPrefixPlaceholder, w.apiPrefix)
	if w.isExternal {
		html = strings.ReplaceAll(html, isExternalPlaceholder, "true")
	} else {
		html = strings.ReplaceAll(html, isExternalPlaceholder, "false")
	}

	// Set CSP header with nonce
	w.setCSPHeader(true)

	// Update Content-Length if it was set
	if w.Header().Get("Content-Length") != "" {
		w.Header().Set("Content-Length", itoa(len(html)))
	}

	// Write headers and body
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.statusCode)
	w.ResponseWriter.Write([]byte(html))
	w.headerWritten = true
}

// setCSPHeader sets the Content-Security-Policy header.
func (w *cspNonceResponseWriter) setCSPHeader(includeNonce bool) {
	var scriptSrc string
	if includeNonce {
		// Use nonce for inline scripts
		// Allow cdn.tailwindcss.com for Tailwind CSS, cdnjs.cloudflare.com for highlight.js,
		// and cdn.jsdelivr.net for Mermaid.js (loaded dynamically for diagram rendering)
		scriptSrc = "'self' 'nonce-" + w.nonce + "' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net"
	} else {
		// For non-HTML responses, no inline scripts needed
		scriptSrc = "'self' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net"
	}

	// Build img-src based on configuration
	// By default, only allow self, data URLs, and blob URLs for security
	// When external images are enabled, also allow HTTPS sources
	imgSrc := "'self' data: blob:"
	if w.allowExternalImages {
		imgSrc = "'self' data: blob: https:"
	}

	csp := "default-src 'self'; " +
		"script-src " + scriptSrc + "; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com; " +
		"img-src " + imgSrc + "; " +
		"font-src 'self' https://fonts.gstatic.com; " +
		"connect-src 'self' ws: wss:; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	w.Header().Set("Content-Security-Policy", csp)
}

// Hijack implements http.Hijacker for WebSocket support.
func (w *cspNonceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, io.ErrUnexpectedEOF
}

// Unwrap returns the underlying ResponseWriter for interface detection.
// This is required for proper compatibility with http.TimeoutHandler and other middleware.
func (w *cspNonceResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// cspNonceMiddlewareOptions contains options for the CSP nonce middleware.
type cspNonceMiddlewareOptions struct {
	config              SecurityConfig
	apiPrefix           string
	allowExternalImages bool
}

// cspNonceMiddlewareWithOptions creates a CSP nonce middleware with additional options.
// It generates a CSP nonce for each request and injects it into HTML responses.
// This allows inline scripts with the nonce attribute while blocking other inline scripts.
// It also injects the API prefix for frontend JavaScript to use.
func cspNonceMiddlewareWithOptions(opts cspNonceMiddlewareOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Generate a unique nonce for this request
			nonce, err := generateCSPNonce()
			if err != nil {
				// Fall back to serving without nonce injection
				// This is a security degradation but better than failing completely
				next.ServeHTTP(w, r)
				return
			}

			// Wrap the response writer to inject nonces, API prefix, and external connection status
			wrapped := &cspNonceResponseWriter{
				ResponseWriter:      w,
				nonce:               nonce,
				apiPrefix:           opts.apiPrefix,
				isExternal:          IsExternalConnection(r),
				allowExternalImages: opts.allowExternalImages,
				config:              opts.config,
			}

			// Serve the request
			next.ServeHTTP(wrapped, r)

			// Flush any buffered HTML content
			wrapped.Flush()
		})
	}
}
