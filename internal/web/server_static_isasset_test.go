package web

import "testing"

func TestIsStaticAsset(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Static assets
		{"/static/app.js", true},
		{"/static/style.css", true},
		{"/images/logo.png", true},
		{"/images/photo.jpg", true},
		{"/images/photo.jpeg", true},
		{"/images/animation.gif", true},
		{"/favicon.ico", true},
		{"/icons/icon.svg", true},
		{"/fonts/font.woff", true},
		{"/fonts/font.woff2", true},
		{"/fonts/font.ttf", true},

		// Non-static paths
		{"/api/sessions", false},
		{"/api/config", false},
		{"/", false},
		{"/index.html", false},
		{"", false},
		{"/api/sessions/123/ws", false},

		// Edge cases
		{".js", false},  // Too short
		{".css", false}, // Too short
		{"/path/to/file", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isStaticAsset(tt.path)
			if result != tt.expected {
				t.Errorf("isStaticAsset(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}
