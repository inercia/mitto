package web

import "testing"

// TestRouteCoversPath pins the DIRECTIONAL wildcard contract that makes the
// mitto-7gta.24 gate work with routes.go's generic sub-path segments (e.g.
// "/api/sessions/{id}/loop/{subPath}" must match the JS SDK's literal
// ".../loop/run-now" and ".../loop/restore") WITHOUT letting an SDK-side
// placeholder absorb an unrelated new sibling route.
func TestRouteCoversPath(t *testing.T) {
	tests := []struct {
		name  string
		route string
		sdk   string
		want  bool
	}{
		{"identical concrete paths", "/api/health", "/api/health", true},
		{"identical with placeholder", "/api/sessions/{}", "/api/sessions/{}", true},
		{"route placeholder covers concrete SDK segment", "/api/sessions/{}", "/api/sessions/abc", true},
		{"wildcard sub-path covers literal sub-action", "/api/sessions/{}/loop/{}", "/api/sessions/{}/loop/run-now", true},
		{"wildcard sub-path covers a second literal sub-action", "/api/sessions/{}/loop/{}", "/api/sessions/{}/loop/restore", true},
		{"SDK placeholder does NOT cover a concrete new sibling route", "/api/issues/brand-new-sibling", "/api/issues/{}", false},
		{"SDK placeholder does NOT cover a concrete route segment", "/api/sessions/running", "/api/sessions/{}", false},
		{"different segment counts never match", "/api/sessions/{}", "/api/sessions/{}/loop", false},
		{"different literal segments do not match", "/api/sessions/abc", "/api/sessions/xyz", false},
		{"differing path length with placeholders still rejected", "/api/a/{}/c", "/api/a/b", false},
		{"empty vs empty", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeCoversPath(tt.route, tt.sdk); got != tt.want {
				t.Errorf("routeCoversPath(%q, %q) = %v, want %v", tt.route, tt.sdk, got, tt.want)
			}
		})
	}
}

// TestNormalizePattern pins the method-stripping and placeholder-collapsing
// contract: Go 1.22 mux "{param}" and JS template "${enc(param)}" segments
// both collapse to "{}", and the HTTP method (if any) is discarded.
func TestNormalizePattern(t *testing.T) {
	tests := []struct {
		name  string
		route string
		want  string
	}{
		{"no method, no params", "/api/health", "/api/health"},
		{"method prefix stripped", "GET /api/sessions", "/api/sessions"},
		{"go mux param collapsed", "GET /api/sessions/{id}", "/api/sessions/{}"},
		{"multiple go mux params collapsed", "DELETE /api/sessions/{id}/images/{imageId}", "/api/sessions/{}/images/{}"},
		{"js template literal collapsed", "/api/sessions/${enc(id)}", "/api/sessions/{}"},
		{"js template literal with nested call collapsed", "/api/sessions/${enc(id)}/images/${enc(imageId)}", "/api/sessions/{}/images/{}"},
		{"trailing slash preserved", "/api/callback/", "/api/callback/"},
		{"method with trailing-slash route", "GET /api/callback/", "/api/callback/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePattern(tt.route); got != tt.want {
				t.Errorf("normalizePattern(%q) = %q, want %q", tt.route, got, tt.want)
			}
		})
	}
}

// TestExtractJSRoutePatterns pins the comment-skipping contract: JSDoc "*"
// continuation lines and "//" line comments must NOT count as coverage, even
// when they mention a real-looking "/api/..." path (e.g. to document that a
// route deliberately does NOT exist) — only live string/template-literal
// call sites count.
func TestExtractJSRoutePatterns(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]bool
	}{
		{
			name:    "double-quoted literal call site",
			content: `call("/api/sessions")`,
			want:    map[string]bool{"/api/sessions": true},
		},
		{
			name:    "template literal with interpolation collapses to placeholder",
			content: "url(`/api/sessions/${enc(id)}/loop/run-now`)",
			want:    map[string]bool{"/api/sessions/{}/loop/run-now": true},
		},
		{
			name: "jsdoc prose mentioning a nonexistent route is ignored",
			content: "/**\n" +
				" * Note: there is no /api/settings route.\n" +
				" */\n" +
				`call("/api/config")`,
			want: map[string]bool{"/api/config": true},
		},
		{
			name:    "line comment referencing a route is ignored",
			content: "// see /api/old-removed-route for history\ncall(\"/api/sessions\")",
			want:    map[string]bool{"/api/sessions": true},
		},
		{
			name:    "no api paths present",
			content: `const x = 1;`,
			want:    map[string]bool{},
		},
		{
			name:    "multiple distinct routes in one file",
			content: "call(\"/api/sessions\")\ncall(\"/api/health\")\n",
			want:    map[string]bool{"/api/sessions": true, "/api/health": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSRoutePatterns(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("extractJSRoutePatterns() = %v, want %v", got, tt.want)
			}
			for k := range tt.want {
				if !got[k] {
					t.Errorf("extractJSRoutePatterns() missing expected pattern %q; got %v", k, got)
				}
			}
		})
	}
}

// TestMergeJSRouteMatches pins the source-file-list aggregation contract
// used for the gate's error messages: distinct files append (comma-joined,
// deduped), and a pattern already attributed to a file is not duplicated.
func TestMergeJSRouteMatches(t *testing.T) {
	dst := map[string]string{}
	mergeJSRouteMatches(dst, map[string]bool{"/api/sessions": true}, "sessions.js")
	mergeJSRouteMatches(dst, map[string]bool{"/api/sessions": true}, "queue.js")
	mergeJSRouteMatches(dst, map[string]bool{"/api/sessions": true}, "sessions.js") // duplicate file, no-op

	want := "sessions.js,queue.js"
	if got := dst["/api/sessions"]; got != want {
		t.Errorf("dst[/api/sessions] = %q, want %q", got, want)
	}
}

// TestParseExemptions pins the exemptions-file contract: blank lines and
// "#"-only lines are ignored, every real entry MUST carry a "# rationale"
// comment or parsing fails with a line-numbered error, and valid entries
// normalize the same way routes do.
func TestParseExemptions(t *testing.T) {
	t.Run("valid entries with rationale", func(t *testing.T) {
		content := "# header comment\n\n" +
			"/api/health  # liveness probe, not SDK-consumed\n" +
			"GET /api/logout  # go-sdk-only cookie logout\n"
		got, err := parseExemptions(content)
		if err != nil {
			t.Fatalf("parseExemptions() error = %v, want nil", err)
		}
		want := map[string]bool{"/api/health": true, "/api/logout": true}
		if len(got) != len(want) {
			t.Fatalf("parseExemptions() = %v, want %v", got, want)
		}
		for k := range want {
			if !got[k] {
				t.Errorf("parseExemptions() missing %q; got %v", k, got)
			}
		}
	})

	t.Run("bare route without rationale fails loudly", func(t *testing.T) {
		content := "/api/does-not-need-a-real-route\n"
		_, err := parseExemptions(content)
		if err == nil {
			t.Fatal("parseExemptions() error = nil, want an error for a missing '# rationale' comment")
		}
	})

	t.Run("empty content yields empty set", func(t *testing.T) {
		got, err := parseExemptions("")
		if err != nil {
			t.Fatalf("parseExemptions() error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Errorf("parseExemptions() = %v, want empty", got)
		}
	})
}
