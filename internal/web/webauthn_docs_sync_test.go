package web

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configPkg "github.com/inercia/mitto/internal/config"
)

// docsPasskeySlug mirrors GitHub's markdown heading-anchor algorithm closely
// enough for this test's fixed ASCII headings: lowercase, drop everything
// outside [a-z0-9 -], then join the remaining whitespace-separated words
// with '-'. Used to pin the ext-access.md cross-link against the literal
// web/README.md heading text instead of hardcoding the anchor twice.
func docsPasskeySlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == ' ':
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), "-")
}

// TestDocsPasskeySectionExistsWithAccurateContent pins the documentation
// increment for mitto-4mz.8 (Document passkey / WebAuthn support for
// External Access) against the real implementation, so a future rename of
// the webauthn config keys, the /api/auth-info "passkey" flag, or the
// cross-link heading breaks this test instead of the docs silently drifting.
// Same docs↔code sync convention as TestDocsGoroutineTriageSectionExists
// (webinterface_docs_sync_test.go) and internal/prompts/docs_sync_test.go.
func TestDocsPasskeySectionExistsWithAccurateContent(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	webReadme := webInterfaceDocsReadFile(t, filepath.Join(root, "docs", "config", "web", "README.md"))
	extAccess := webInterfaceDocsReadFile(t, filepath.Join(root, "docs", "config", "ext-access.md"))
	defaultYAML := webInterfaceDocsReadFile(t, filepath.Join(root, "config", "config.default.yaml"))

	const heading = "Passkey (WebAuthn) Authentication"
	if !strings.Contains(webReadme, "### "+heading) {
		t.Fatalf("docs/config/web/README.md: missing heading %q", "### "+heading)
	}

	// Sanity-check the slug algorithm against a hyphenated heading already
	// cross-linked elsewhere in this doc set before trusting it to derive
	// the expected anchor below.
	if got := docsPasskeySlug("Built-in External Listener"); got != "built-in-external-listener" {
		t.Fatalf("docsPasskeySlug sanity check failed: got %q", got)
	}
	wantAnchor := "web/README.md#" + docsPasskeySlug(heading)

	for _, marker := range []string{
		"webauthn:", "enabled: true", "rp_id:", "rp_origin:", "rp_display_name:",
		"web.hooks.external_address", "stable https URL",
		"GET /api/auth-info", "passkey` boolean",
		"navigator.credentials.create()", "Conditional Create",
		"Origin-match constraint", "localhost",
	} {
		if !strings.Contains(webReadme, marker) {
			t.Errorf("docs/config/web/README.md §Passkey: missing marker %q", marker)
		}
	}

	if !strings.Contains(extAccess, wantAnchor) {
		t.Errorf("docs/config/ext-access.md: expected cross-link %q not found", wantAnchor)
	}
	for _, marker := range []string{
		"Consider Passkey (WebAuthn) Authentication", "stable https URL", "localhost",
	} {
		if !strings.Contains(extAccess, marker) {
			t.Errorf("docs/config/ext-access.md: missing marker %q", marker)
		}
	}

	for _, marker := range []string{
		"webauthn:", "enabled: true", "rp_display_name: Mitto",
		"web.hooks.external_address", "Conditional Create", "localhost",
	} {
		if !strings.Contains(defaultYAML, marker) {
			t.Errorf("config/config.default.yaml §webauthn: missing marker %q", marker)
		}
	}
}

// TestDocsPasskeyConfigFieldsMatchWebAuthnConfigStruct pins the yaml keys the
// docs advertise (enabled, rp_id, rp_origin, rp_display_name) against the
// real configPkg.WebAuthnConfig struct tags, so a silent rename in
// internal/config/config.go breaks this test instead of the docs drifting.
func TestDocsPasskeyConfigFieldsMatchWebAuthnConfigStruct(t *testing.T) {
	typ := reflect.TypeOf(configPkg.WebAuthnConfig{})
	wantFieldTags := map[string]string{
		"Enabled":       "enabled",
		"RPID":          "rp_id",
		"RPOrigin":      "rp_origin",
		"RPDisplayName": "rp_display_name",
	}
	for field, wantTag := range wantFieldTags {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Errorf("configPkg.WebAuthnConfig: missing field %s (docs reference yaml key %q)", field, wantTag)
			continue
		}
		yamlTag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if yamlTag != wantTag {
			t.Errorf("configPkg.WebAuthnConfig.%s yaml tag = %q, want %q", field, yamlTag, wantTag)
		}
	}
}

// TestDocsPasskeyRPDerivationMatchesDocumentedBehavior pins the doc's factual
// claims about Relying Party derivation (docs/config/web/README.md
// §Passkey) against the real DeriveWebAuthnRP behavior: a stable https
// external_address derives both rp_id/rp_origin, and a non-https (e.g.
// ephemeral tunnel-style) address is rejected rather than silently deriving
// an unstable RP.
func TestDocsPasskeyRPDerivationMatchesDocumentedBehavior(t *testing.T) {
	rpID, rpOrigin, err := configPkg.DeriveWebAuthnRP("https://mitto.example.com", "", "")
	if err != nil {
		t.Fatalf("unexpected error deriving RP from a stable https address: %v", err)
	}
	if rpID != "mitto.example.com" || rpOrigin != "https://mitto.example.com" {
		t.Errorf("rpID/rpOrigin = %q/%q, want %q/%q", rpID, rpOrigin, "mitto.example.com", "https://mitto.example.com")
	}

	if _, _, err := configPkg.DeriveWebAuthnRP("http://ephemeral-tunnel.example.com", "", ""); err == nil {
		t.Error("expected DeriveWebAuthnRP to reject a non-https address, as docs claim (\"stable https URL\" requirement)")
	}
}
