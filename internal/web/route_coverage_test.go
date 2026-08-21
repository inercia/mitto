package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/web/middleware"
	"github.com/inercia/mitto/pkg/api"
)

// TestRouteCoverage_SDKsOrExempt is the mitto-7gta.24 gate: every route
// declared in the LIVE table returned by apiRoutes() must be reachable
// through the Go SDK (pkg/api.RouteCoverage), the JS SDK
// (web/static/sdk/resources/*.js + core/endpoints.js), or an explicit,
// commented exemption in testdata/route_coverage_exemptions.txt. It also
// fails on stale exemptions (no longer needed) and on SDK paths that no
// longer correspond to any declared server route, so drift is caught in
// both directions.
//
// This lives in package web (not pkg/api) because pkg/api must not import
// internal/ packages, while internal/web may import pkg/api without
// creating a cycle (pkg/api never imports internal/web).
func TestRouteCoverage_SDKsOrExempt(t *testing.T) {
	routes := liveRoutePatterns(t)
	goCovered := goSDKCoveredPatterns()
	jsCovered := jsSDKCoveredPatterns(t)
	exempt := loadExemptions(t)

	routeNorms := make([]string, len(routes))
	for i, route := range routes {
		routeNorms[i] = normalizePattern(route)
	}

	for i, route := range routes {
		norm := routeNorms[i]
		switch {
		case anyRouteCovers(norm, goCovered), anyRouteCoversKeyed(norm, jsCovered):
			// Covered by at least one SDK.
		case exempt[norm]:
			// Explicitly exempted; fine.
		default:
			t.Errorf("route %q (normalized %q) is not covered by the Go SDK, the JS SDK, "+
				"or testdata/route_coverage_exemptions.txt — add an SDK method or a "+
				"commented exemption", route, norm)
		}
	}

	for norm := range exempt {
		if !containsPattern(routeNorms, norm) {
			t.Errorf("stale exemption %q in testdata/route_coverage_exemptions.txt: "+
				"no live route in routes.go normalizes to this pattern anymore", norm)
		}
	}

	// Reverse direction: every JS-SDK-covered path must correspond to a real
	// server route (catches SDK methods pointing at removed/renamed routes).
	// A route-side "{}" segment (a Go 1.22 mux path parameter) matches ANY
	// concrete SDK segment in the same position (e.g. routes.go's
	// "/api/sessions/{id}/loop/{subPath}" matches the JS SDK's literal
	// ".../loop/run-now" and ".../loop/restore") — see routeCoversPath.
	for norm, srcs := range jsCovered {
		covered := false
		for _, routeNorm := range routeNorms {
			if routeCoversPath(routeNorm, norm) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("JS SDK path %q (declared in %s) has no matching route in routes.go", norm, srcs)
		}
	}
}

// containsPattern reports whether norm equals (exact, not wildcard) any
// entry in routeNorms — exemptions are pinned to a specific route shape, not
// wildcard-matched, so a typo'd exemption is caught rather than silently
// matching something else.
func containsPattern(routeNorms []string, norm string) bool {
	for _, r := range routeNorms {
		if r == norm {
			return true
		}
	}
	return false
}

// anyRouteCovers reports whether routeNorm is covered by any key in
// candidates (see routeCoversPath).
func anyRouteCovers(routeNorm string, candidates map[string]bool) bool {
	for c := range candidates {
		if routeCoversPath(routeNorm, c) {
			return true
		}
	}
	return false
}

// anyRouteCoversKeyed is anyRouteCovers for a map[string]string (JS covered
// set, which also carries source-file provenance as the value).
func anyRouteCoversKeyed(routeNorm string, candidates map[string]string) bool {
	for c := range candidates {
		if routeCoversPath(routeNorm, c) {
			return true
		}
	}
	return false
}

// routeCoversPath compares a normalized ROUTE pattern against a normalized
// SDK path segment-by-segment. The wildcard is DIRECTIONAL: only a "{}" on
// the route side matches an arbitrary segment on the SDK side, because a
// route-declared path parameter genuinely subsumes every concrete value the
// SDK may send (routes.go's "/api/sessions/{id}/loop/{subPath}" really does
// serve the JS SDK's ".../loop/run-now" and ".../loop/restore").
//
// The converse is NOT true and must not match: an SDK-side "{}" is a single
// call site with an interpolated value, not a promise to cover every sibling
// path. Treating it as a wildcard let a brand-new concrete route (e.g.
// "/api/issues/brand-new-sibling") be silently absorbed by the SDK's
// pre-existing "/api/issues/{}" entry, defeating the whole point of the gate
// for that entire class of additions. Both paths must have the same segment
// count.
func routeCoversPath(routeNorm, sdkNorm string) bool {
	if routeNorm == sdkNorm {
		return true
	}
	rs := strings.Split(routeNorm, "/")
	ss := strings.Split(sdkNorm, "/")
	if len(rs) != len(ss) {
		return false
	}
	for i := range rs {
		if rs[i] == ss[i] || rs[i] == "{}" {
			continue
		}
		return false
	}
	return true
}

// liveRoutePatterns builds the declarative route table via the real
// apiRoutes() method (non-nil authMgr so /api/login and /api/logout are
// included) and returns "METHOD PATTERN" strings (METHOD omitted when the
// route has no method qualifier). Only the pattern/method fields are read;
// handler values bind to a nil-fielded *Server without being invoked.
func liveRoutePatterns(t *testing.T) []string {
	t.Helper()
	s := &Server{}
	authMgr := middleware.NewAuthManager(nil)
	csrfMgr := middleware.NewCSRFManager()
	routes := s.apiRoutes(authMgr, csrfMgr, nil)
	if len(routes) == 0 {
		t.Fatal("apiRoutes returned no routes")
	}
	out := make([]string, len(routes))
	for i, rt := range routes {
		if rt.method != "" {
			out[i] = rt.method + " " + rt.pattern
		} else {
			out[i] = rt.pattern
		}
	}
	return out
}

// routeCoverageParamRe matches Go 1.22 mux path parameters like
// "{id}"/"{imageId}". (contract_test.go already declares a package-level
// pathParamRe with a looser pattern for a different purpose — this gate
// uses its own name to avoid colliding with that declaration.)
var routeCoverageParamRe = regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)

// jsTemplateParamRe matches JS template-literal interpolations like
// "${enc(id)}" or "${enc(imageId)}".
var jsTemplateParamRe = regexp.MustCompile(`\$\{[^}]+\}`)

// normalizePattern collapses a route to its path-SHAPE only (the HTTP
// method, if any, is discarded — see the Plan's design decision 3: many
// routes.go entries omit "method" and dispatch on method internally, e.g.
// /api/sessions/{id}/queue serves GET/POST/DELETE through one wrapper, so a
// method-level matrix would encode handler internals rather than reachable
// surface). A server route's named path parameter ("{id}", "{imageId}", …)
// and a JS template-literal interpolation ("${enc(id)}") both collapse to
// "{}" so they compare equal for the same position. A trailing "/" (prefix
// route, e.g. "/api/callback/") is preserved verbatim since it changes
// matching semantics.
func normalizePattern(route string) string {
	path := route
	if parts := strings.SplitN(route, " ", 2); len(parts) == 2 {
		path = parts[1]
	}
	path = routeCoverageParamRe.ReplaceAllString(path, "{}")
	path = jsTemplateParamRe.ReplaceAllString(path, "{}")
	return path
}

// goSDKCoveredPatterns derives the Go SDK's covered-path set from the
// exported pkg/api.RouteCoverage map (route -> *Client method name).
func goSDKCoveredPatterns() map[string]bool {
	out := make(map[string]bool, len(api.RouteCoverage))
	for route := range api.RouteCoverage {
		out[normalizePattern(route)] = true
	}
	return out
}

// jsSourceRouteRe extracts "/api/..." string/template literals from JS
// source: a leading quote or backtick, then "/api/" up to the closing quote
// (double-quoted) or backtick/`${` (template literal), stopping at
// whitespace so trailing query-string comments in JSDoc don't leak in.
var jsSourceRouteRe = regexp.MustCompile("[\"`](/api/[^\"`\\s]*)[\"`]")

// jsSDKCoveredPatterns scans the JS SDK's REST resource modules (raw
// relative path templates, deliberately not built through
// core/endpoints.js — see resources/sessions.js's header) plus
// core/endpoints.js's registry for "/api/..." literals, normalizes them,
// and returns a map of normalized pattern -> comma-joined source file list
// (for error messages). Test files (*.test.js) are excluded.
func jsSDKCoveredPatterns(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "web", "static", "sdk")
	dirs := []string{
		filepath.Join(root, "resources"),
	}
	files := []string{filepath.Join(root, "core", "endpoints.js")}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read JS SDK resources dir: %v", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") || strings.HasSuffix(e.Name(), ".test.js") {
				continue
			}
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	out := make(map[string]string)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		rel := filepath.Base(f)
		mergeJSRouteMatches(out, extractJSRoutePatterns(string(data)), rel)
	}
	return out
}

// extractJSRoutePatterns is the pure, file-content-only core of
// jsSDKCoveredPatterns: it scans JS source text for "/api/..." string and
// template-literal patterns, normalizes them, and returns the set of
// distinct normalized patterns found. Split out from jsSDKCoveredPatterns so
// the comment-skipping and extraction logic can be unit-tested directly
// against literal snippets, independent of the real SDK source tree.
func extractJSRoutePatterns(content string) map[string]bool {
	out := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		// Skip comment lines (JSDoc "* ..." continuations and "//"): these
		// sometimes reference a route in PROSE precisely to say it does NOT
		// exist (e.g. config.js's "there is no /api/settings route" note) —
		// only real call sites should count as coverage.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, m := range jsSourceRouteRe.FindAllStringSubmatch(line, -1) {
			out[normalizePattern(m[1])] = true
		}
	}
	return out
}

// mergeJSRouteMatches folds a single file's extracted pattern set into the
// aggregate pattern -> source-file-list map used for gate error messages.
func mergeJSRouteMatches(dst map[string]string, found map[string]bool, rel string) {
	for norm := range found {
		if dst[norm] == "" {
			dst[norm] = rel
		} else if !strings.Contains(dst[norm], rel) {
			dst[norm] += "," + rel
		}
	}
}

// exemptionLineRe requires a rationale comment on every exemption line:
// "<route>  # <why>". A bare route with no comment is a gate failure —
// forces every exemption to be justified inline, not bulk-added.
var exemptionLineRe = regexp.MustCompile(`^(\S+(?:\s+\S+)?)\s+#\s*(\S.*)$`)

// loadExemptions reads testdata/route_coverage_exemptions.txt: blank lines
// and lines starting with "#" are ignored; every other line must match
// exemptionLineRe (route, optionally "METHOD path", then a "# rationale"
// comment) or the gate fails loudly rather than silently accepting an
// unjustified exemption.
func loadExemptions(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join("testdata", "route_coverage_exemptions.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("read %s: %v", path, err)
	}
	out, parseErr := parseExemptions(string(data))
	if parseErr != nil {
		t.Fatalf("%s: %v", path, parseErr)
	}
	return out
}

// parseExemptions is the pure, file-content-only core of loadExemptions: it
// parses the exemptions-file text and returns the normalized-pattern set, or
// an error identifying the first line that fails the "# rationale" contract.
// Split out so the parsing/validation contract can be unit-tested directly
// against literal snippets, independent of the on-disk testdata file.
func parseExemptions(content string) (map[string]bool, error) {
	out := make(map[string]bool)
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := exemptionLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: exemption line missing required '# rationale' comment: %q", i+1, line)
		}
		out[normalizePattern(m[1])] = true
	}
	return out, nil
}
