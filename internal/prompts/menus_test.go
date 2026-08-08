package prompts

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseMenuTokens(t *testing.T) {
	cases := []struct {
		name         string
		menus        string
		wantIncluded []string
		wantExcluded []string
	}{
		{"empty", "", nil, nil},
		{"single", "prompts", []string{"prompts"}, nil},
		{"multiple", "prompts, conversation", []string{"prompts", "conversation"}, nil},
		{"exclusion", "prompts, !promptsLoop", []string{"prompts"}, []string{"promptsLoop"}},
		{"bare exclamation dropped", "prompts, !", []string{"prompts"}, nil},
		{"whitespace", "  prompts  ,  conversation  ", []string{"prompts", "conversation"}, nil},
		{"blank entries dropped", "prompts,,conversation", []string{"prompts", "conversation"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			included, excluded := ParseMenuTokens(c.menus)
			if !reflect.DeepEqual(included, c.wantIncluded) {
				t.Errorf("included = %#v, want %#v", included, c.wantIncluded)
			}
			if !reflect.DeepEqual(excluded, c.wantExcluded) {
				t.Errorf("excluded = %#v, want %#v", excluded, c.wantExcluded)
			}
		})
	}
}

func TestUnknownMenuTokens(t *testing.T) {
	cases := []struct {
		name  string
		menus string
		want  []string
	}{
		{"empty", "", nil},
		{"all known", "prompts, conversation, beadsIssues, beadsList, promptsLoop", nil},
		{"internal sentinel", "internal", nil},
		{"valid exclusion", "prompts, !promptsLoop", nil},
		{"typo plural", "prompts, conversations", []string{"conversations"}},
		{"typo exclusion", "prompts, !typo", []string{"typo"}},
		{"dedup", "conversations, conversations", []string{"conversations"}},
		{"sorted", "ztypo, atypo", []string{"atypo", "ztypo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UnknownMenuTokens(c.menus)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("UnknownMenuTokens(%q) = %#v, want %#v", c.menus, got, c.want)
			}
		})
	}
}

// TestWarnUnknownMenus_LogsForUnknownTokens captures slog output to assert
// WarnUnknownMenus warns exactly when UnknownMenuTokens is non-empty, and
// names the prompt, path and offending token(s) (acceptance criterion #1).
func TestWarnUnknownMenus_LogsForUnknownTokens(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	WarnUnknownMenus("test-warn-unique-1", "some/path.prompt.yaml", "prompts, conversations")

	out := buf.String()
	for _, want := range []string{"test-warn-unique-1", "some/path.prompt.yaml", "conversations"} {
		if !strings.Contains(out, want) {
			t.Errorf("WarnUnknownMenus log output missing %q; got: %s", want, out)
		}
	}
}

// TestWarnUnknownMenus_SilentForValidMenus covers acceptance criteria #2/#3:
// "internal" and a valid exclusion token must never warn.
func TestWarnUnknownMenus_SilentForValidMenus(t *testing.T) {
	for _, menus := range []string{"internal", "prompts, !promptsLoop", "prompts, conversation"} {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

		WarnUnknownMenus("test-warn-unique-2-"+menus, "some/path.prompt.yaml", menus)

		slog.SetDefault(prev)
		if buf.Len() != 0 {
			t.Errorf("WarnUnknownMenus(%q) logged unexpectedly: %s", menus, buf.String())
		}
	}
}

// TestWarnUnknownMenus_DedupesPerKey asserts the same (prompt, path, tokens)
// combination only logs once per process, mirroring WarnDeprecatedMittoVars.
func TestWarnUnknownMenus_DedupesPerKey(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	WarnUnknownMenus("test-warn-dedupe", "dedupe/path.prompt.yaml", "typo")
	WarnUnknownMenus("test-warn-dedupe", "dedupe/path.prompt.yaml", "typo")

	if n := strings.Count(buf.String(), "test-warn-dedupe"); n != 1 {
		t.Errorf("expected exactly 1 warning, got %d; log: %s", n, buf.String())
	}
}

// menuParamTypesKeyRe matches an object-literal key line inside
// MENU_PARAM_TYPES, e.g. `  beadsIssues: ["beadsId", "beadsTitle"],`.
var menuParamTypesKeyRe = regexp.MustCompile(`^\s*(\w+):`)

// TestMenus_KnownMenusMatchesFrontendRegistry pins the docs↔code contract for
// mitto-rjg6: KnownMenus (the Go source of truth) must contain exactly the
// same keys as MENU_PARAM_TYPES in web/static/utils/prompts.js (the frontend
// source of truth for menu-supplied param types). "internal" is deliberately
// excluded from the frontend registry (it has no menu surface to satisfy) and
// is asserted separately. Same docs/code-sync convention as
// internal/web/webinterface_docs_sync_test.go and internal/cel/docs_sync_test.go.
func TestMenus_KnownMenusMatchesFrontendRegistry(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/prompts/menus_test.go
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	jsPath := filepath.Join(root, "web", "static", "utils", "prompts.js")
	data, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read %s: %v", jsPath, err)
	}

	const marker = "export const MENU_PARAM_TYPES = {"
	start := strings.Index(string(data), marker)
	if start == -1 {
		t.Fatalf("%s: marker %q not found", jsPath, marker)
	}
	body := string(data)[start+len(marker):]
	end := strings.Index(body, "\n};")
	if end == -1 {
		t.Fatalf("%s: closing '};' for MENU_PARAM_TYPES not found", jsPath)
	}
	body = body[:end]

	var jsKeys []string
	for _, line := range strings.Split(body, "\n") {
		if m := menuParamTypesKeyRe.FindStringSubmatch(line); m != nil {
			jsKeys = append(jsKeys, m[1])
		}
	}
	if len(jsKeys) == 0 {
		t.Fatalf("%s: no keys extracted from MENU_PARAM_TYPES body: %q", jsPath, body)
	}

	sort.Strings(jsKeys)
	want := make([]string, len(KnownMenus))
	copy(want, KnownMenus)
	sort.Strings(want)

	if !reflect.DeepEqual(jsKeys, want) {
		t.Errorf("MENU_PARAM_TYPES keys %v != KnownMenus %v (keep both registries in sync)", jsKeys, want)
	}
	for _, k := range jsKeys {
		if k == MenuInternal {
			t.Errorf("MENU_PARAM_TYPES unexpectedly contains %q; it is a Go-only dispatch sentinel with no frontend menu surface", MenuInternal)
		}
	}
}

// TestParsePromptFile_WithUnknownMenuToken_WarnsButLoads is an end-to-end
// check of acceptance criterion #1 for mitto-rjg6: a prompt file with an
// unrecognised menus token (the mitto-kazd plural typo) must still parse
// successfully (non-fatal) while ParsePromptFile emits a WARN naming the
// prompt, its file path, and the offending token.
func TestParsePromptFile_WithUnknownMenuToken_WarnsButLoads(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	data := []byte(`name: "Typo Menu Prompt"
menus: prompts, conversations
prompt: |
  Body.
`)
	path := "typo-menu.prompt.yaml"
	prompt, err := ParsePromptFile(path, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed for an unrecognised (but non-fatal) menus token: %v", err)
	}
	if prompt.Name != "Typo Menu Prompt" {
		t.Errorf("Name = %q, want %q (prompt must still load)", prompt.Name, "Typo Menu Prompt")
	}
	if prompt.Menus != "prompts, conversations" {
		t.Errorf("Menus = %q, want the raw string preserved verbatim", prompt.Menus)
	}

	out := buf.String()
	for _, want := range []string{"Typo Menu Prompt", path, "conversations"} {
		if !strings.Contains(out, want) {
			t.Errorf("ParsePromptFile did not warn as expected; missing %q in log: %s", want, out)
		}
	}
}

// TestParsePromptFile_WithInternalMenu_NoWarning and the exclusion-token case
// cover acceptance criteria #2/#3 through the real ParsePromptFile entry
// point (not just the WarnUnknownMenus unit tested above).
func TestParsePromptFile_WithInternalMenu_NoWarning(t *testing.T) {
	for _, tc := range []struct {
		name  string
		menus string
	}{
		{"internal sentinel", "internal"},
		{"valid exclusion", "prompts, !promptsLoop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			data := []byte("name: \"Valid Menu Prompt\"\nmenus: " + tc.menus + "\nprompt: |\n  Body.\n")
			if _, err := ParsePromptFile("valid-menu.prompt.yaml", data, time.Now()); err != nil {
				t.Fatalf("ParsePromptFile failed: %v", err)
			}
			if buf.Len() != 0 {
				t.Errorf("ParsePromptFile(menus=%q) warned unexpectedly: %s", tc.menus, buf.String())
			}
		})
	}
}

// docsPromptsMdMenusMarkers pins the mitto-rjg6 documentation increment in
// docs/config/prompts.md against silent drift: the `internal` sentinel and
// the "Menu Token Validation" section (heading, warning behaviour, and the
// two guardian-test callouts) must all remain present. Same docs↔code sync
// convention as TestDocsFragmentsSectionExists (docs_fragments_sync_test.go)
// and internal/web/webinterface_docs_sync_test.go.
func TestDocsMenusSection_DocumentsInternalSentinelAndValidation(t *testing.T) {
	root := repoRootForTest(t)
	doc := readFileForTest(t, filepath.Join(root, "docs", "config", "prompts.md"))

	const wantHeading = "### Menu Token Validation"
	if !strings.Contains(doc, wantHeading) {
		t.Errorf("docs/config/prompts.md: missing heading %q", wantHeading)
	}

	for _, marker := range []string{
		// The internal sentinel, documented as a Go-only token.
		"`internal`",
		"Go-only sentinel",
		// Warning behaviour and its non-fatal nature.
		"unrecognised token(s); prompt will not appear in the intended menu",
		"does **not** fail prompt loading",
		// Source-of-truth pointers.
		"internal/prompts/menus.go",
		"KnownMenuTokens",
		"WarnUnknownMenus",
		// Guardian-test callouts, mirroring the code comments on the tests
		// themselves (menus_test.go / prompts_test.go).
		"TestBuiltinPrompts_MenusTokensRecognized",
		"TestMenus_KnownMenusMatchesFrontendRegistry",
		"MENU_PARAM_TYPES",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("docs/config/prompts.md §Menu Token Validation: missing marker %q", marker)
		}
	}
}
