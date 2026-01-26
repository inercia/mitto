// Package web provides embedded static assets for the Mitto web interface.
package web

import "embed"

// StaticFS contains the embedded static files for the web interface.
// These files are served by the HTTP server when running `mitto web`.
// Updated: 2026-01-25
//
//go:embed static/*
var StaticFS embed.FS
