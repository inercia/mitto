package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsAPISDKPublicExportsExistInSource pins the acceptance criteria for
// mitto-7gta.21 (consumer-facing docs/api/ reference): every symbol name the
// new docs advertise as part of the SDK's public surface
// (web/static/sdk/index.js) must still be a real export in that file, and
// must actually be documented somewhere under docs/api/, so a silent
// rename/removal in the SDK — or an undocumented new/renamed export — breaks
// this test instead of the docs silently drifting. Same docs↔code sync
// convention as internal/prompts/docs_sync_test.go and
// TestDocsGoroutineTriageSectionExists (this package).
func TestDocsAPISDKPublicExportsExistInSource(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	indexJS := webInterfaceDocsReadFile(t, filepath.Join(root, "web", "static", "sdk", "index.js"))
	docs := sdkDocsConcat(t, root)

	wantExports := []string{
		"VERSION", "createClient",
		"MittoError", "ConfigError", "MittoApiError", "MittoAuthError", "MittoNetworkError",
		"browserEnv", "browserCookieReader",
		"noneAuth", "sharedTokenAuth", "browserCookieAuth",
		"createSessionStream", "SessionStream", "createEventsStream", "EventsStream",
		"createSeqTracker", "isSeqDuplicate", "markSeqSeen", "getMaxSeq",
		"isStaleClientState", "isTerminalSessionError",
		"createMemorySeqStore", "createStorageSeqStore",
		"generatePromptId", "createMemoryPendingPromptStore", "createStoragePendingPromptStore",
		"EVENTS", "COMMANDS", "LEGACY_EVENTS", "isKnownEventType", "isCommandType",
		"createTtlCache", "keyForParams", "withIssueCaches",
	}
	for _, name := range wantExports {
		if !strings.Contains(indexJS, name) {
			t.Errorf("web/static/sdk/index.js: expected export %q no longer present (docs/api/ documents it)", name)
		}
		if !strings.Contains(docs, name) {
			t.Errorf("docs/api/*.md: SDK export %q is not documented anywhere", name)
		}
	}

	// The returned client object's resource-namespace property names,
	// documented in docs/api/client.md's "returned client shape" section.
	wantClientProps := []string{
		"sessions", "prompts", "processors", "shortcuts", "issues",
		"serverConfig", "files", "images", "dashboard", "misc",
		"workspaces", "acpServers", "agents", "sessionStream", "eventsStream",
	}
	for _, name := range wantClientProps {
		if !strings.Contains(indexJS, name) {
			t.Errorf("web/static/sdk/index.js createClient(): expected client property %q not found", name)
		}
	}
}

// TestDocsAPIClientOptionsMatchConfigAllowedKeys pins docs/api/client.md's
// createClient() option table against core/config.js's ALLOWED_KEYS.
func TestDocsAPIClientOptionsMatchConfigAllowedKeys(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	configJS := webInterfaceDocsReadFile(t, filepath.Join(root, "web", "static", "sdk", "core", "config.js"))
	clientDoc := webInterfaceDocsReadFile(t, filepath.Join(root, "docs", "api", "client.md"))

	wantKeys := []string{
		"baseUrl", "apiPrefix", "fetch", "WebSocket", "storage",
		"auth", "logger", "onUnauthorized", "wsBaseUrl",
	}
	for _, key := range wantKeys {
		if !strings.Contains(configJS, `"`+key+`"`) {
			t.Errorf("web/static/sdk/core/config.js ALLOWED_KEYS: expected key %q not found as a string literal", key)
		}
		if !strings.Contains(clientDoc, key) {
			t.Errorf("docs/api/client.md: createClient() option %q is not documented", key)
		}
	}
}

// TestDocsAPIErrorTaxonomyMatchesSource pins docs/api/errors.md's class
// names and status->code table against core/errors.js.
func TestDocsAPIErrorTaxonomyMatchesSource(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	errorsJS := webInterfaceDocsReadFile(t, filepath.Join(root, "web", "static", "sdk", "core", "errors.js"))
	errorsDoc := webInterfaceDocsReadFile(t, filepath.Join(root, "docs", "api", "errors.md"))

	classes := []string{"MittoError", "ConfigError", "MittoApiError", "MittoAuthError", "MittoNetworkError"}
	for _, name := range classes {
		if !strings.Contains(errorsJS, "class "+name) {
			t.Errorf("web/static/sdk/core/errors.js: expected class %q not found", name)
		}
		if !strings.Contains(errorsDoc, name) {
			t.Errorf("docs/api/errors.md: error class %q is not documented", name)
		}
	}

	codes := []string{
		"invalid_config", "network_error",
		"bad_request", "unauthenticated", "forbidden", "not_found",
		"method_not_allowed", "conflict", "too_large", "rate_limited",
		"unavailable", "server_error",
	}
	for _, code := range codes {
		if !strings.Contains(errorsJS, `"`+code+`"`) {
			t.Errorf("web/static/sdk/core/errors.js: expected code %q not found as a string literal", code)
		}
		if !strings.Contains(errorsDoc, code) {
			t.Errorf("docs/api/errors.md: error code %q is not documented", code)
		}
	}
}

// TestDocsAPIInternalLinksResolve walks every relative markdown link in
// docs/api/*.md and asserts its target exists on disk, so a future rename of
// a linked doc (in docs/api/ or docs/devel/, docs/config/, etc.) breaks this
// test instead of leaving a silently broken link.
func TestDocsAPIInternalLinksResolve(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	apiDir := filepath.Join(root, "docs", "api")

	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read docs/api: %v", err)
	}

	linkRe := regexp.MustCompile(`\]\(([^)]+)\)`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		content := webInterfaceDocsReadFile(t, filepath.Join(apiDir, e.Name()))
		for _, m := range linkRe.FindAllStringSubmatch(content, -1) {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				continue
			}
			if idx := strings.Index(target, "#"); idx >= 0 {
				target = target[:idx]
			}
			if target == "" {
				continue // same-file anchor
			}
			resolved := filepath.Join(apiDir, target)
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s: broken link target %q (resolved %s): %v", e.Name(), m[1], resolved, err)
			}
		}
	}
}

// sdkDocsConcat concatenates every docs/api/*.md file's content, for
// existence checks that don't care which page mentions a given symbol.
func sdkDocsConcat(t *testing.T, root string) string {
	t.Helper()
	apiDir := filepath.Join(root, "docs", "api")
	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read docs/api: %v", err)
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		sb.WriteString(webInterfaceDocsReadFile(t, filepath.Join(apiDir, e.Name())))
		sb.WriteString("\n")
	}
	return sb.String()
}
