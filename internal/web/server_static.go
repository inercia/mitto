package web

import (
	"io/fs"
	"net/http"

	"github.com/inercia/mitto/internal/logging"
)

// staticFileHandler wraps the file server to handle content types and security.
// It returns a minimal 404 for unknown files to avoid leaking information.
//
// Security considerations:
//   - Returns generic 404 for missing files (no path disclosure)
//   - Disables caching to ensure fresh content during development
//   - Logs auth-related file requests at INFO level for debugging
func (s *Server) staticFileHandler(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := logging.Web()

		// Clean the path
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Remove leading slash for fs.Open
		fsPath := path
		if len(fsPath) > 0 && fsPath[0] == '/' {
			fsPath = fsPath[1:]
		}

		// Always log at INFO level for auth-related files to debug login issues
		isAuthFile := fsPath == "auth.html" || fsPath == "auth.js" || fsPath == "tailwind-config-auth.js"
		if isAuthFile {
			logger.Info("STATIC: Auth file request",
				"url_path", r.URL.Path,
				"fs_path", fsPath,
				"remote_addr", r.RemoteAddr,
			)
		} else {
			logger.Debug("Static file request",
				"url_path", r.URL.Path,
				"fs_path", fsPath,
				"remote_addr", r.RemoteAddr,
			)
		}

		// Check if file exists before serving
		f, err := staticFS.Open(fsPath)
		if err != nil {
			if isAuthFile {
				logger.Info("STATIC: Auth file NOT FOUND",
					"fs_path", fsPath,
					"error", err,
				)
			} else {
				logger.Debug("Static file not found",
					"fs_path", fsPath,
					"error", err,
				)
			}
			// Return minimal 404 - don't reveal application type
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		f.Close()

		if isAuthFile {
			logger.Info("STATIC: Serving auth file",
				"fs_path", fsPath,
			)
		} else {
			logger.Debug("Serving static file",
				"fs_path", fsPath,
			)
		}

		// Set correct Content-Type for web app manifest.
		// Go's http.FileServer serves .json as application/json, but
		// Chromium requires application/manifest+json for PWA installability.
		if fsPath == "manifest.json" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}

		// Set cache headers for static assets
		// During active development, we disable caching for all our own assets
		// to ensure users always get the latest version. Only external CDN
		// resources (loaded via <script src="https://..."> in HTML) can be cached
		// by the browser since we don't control those headers anyway.
		//
		// This is especially important because:
		// - HTML pages contain injected values (API prefix, CSP nonces)
		// - JS/CSS files change frequently during development
		// - Cached stale assets cause hard-to-debug issues (like wrong API paths)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		fileServer.ServeHTTP(w, r)
	})
}

// isStaticAsset returns true if the path is a static asset.
// Used by logging middleware to reduce log verbosity for asset requests.
func isStaticAsset(path string) bool {
	staticExtensions := []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg", ".woff", ".woff2", ".ttf"}
	for _, ext := range staticExtensions {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return false
}
