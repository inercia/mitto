package prompts

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestRulesDocTemplateFuncsCatalogueMatchesFuncMap pins the acceptance criteria
// for mitto-eac: the "Useful functions" catalogue in
// .augment/rules/07-prompts.md — the agent-facing short-form reference for
// prompt-authoring — must mention every helper actually registered in
// cel.BuildTemplateFuncMap (the runtime source of truth).
//
// Rationale: the bead's headline missing helper (Dir) shipped in
// BuildTemplateFuncMap and in the user-facing docs/config/prompts.md but silently
// missed the agent-facing rules file, so agents authoring new prompts did not
// know Dir existed and could not use it. This test locks the rules file against
// that class of drift: adding a new helper to BuildTemplateFuncMap without
// mentioning it in the rules file — or removing/renaming one in either surface
// without the other — breaks this test instead of silently degrading the
// prompt-authoring agent's short-form reference.
//
// Pattern mirrors the sibling docs↔code sync tests in this package
// (TestDocsFragmentsSectionExists, internal/cel/docs_sync_test.go) and matches
// the "test that ships with the docs increment" convention documented there.
func TestRulesDocTemplateFuncsCatalogueMatchesFuncMap(t *testing.T) {
	root := repoRootForTest(t)
	rule := readFileForTest(t, filepath.Join(root, ".augment", "rules", "07-prompts.md"))

	// Source of truth: the real FuncMap built from a nil ctx. All keys must
	// appear regardless of the ctx passed to callers, so nil-ctx is fine.
	funcMap := cel.BuildTemplateFuncMap(nil)
	names := make([]string, 0, len(funcMap))
	for name := range funcMap {
		names = append(names, name)
	}
	sort.Strings(names)

	// Missing safeguard: an empty FuncMap would trivially satisfy the loop
	// below and defeat the whole test. Guard the source-of-truth side
	// explicitly so a future refactor that neuters BuildTemplateFuncMap breaks
	// this test loudly.
	if len(names) == 0 {
		t.Fatal("cel.BuildTemplateFuncMap(nil) returned an empty map — source-of-truth guard tripped")
	}

	for _, name := range names {
		// Word-boundary match so a helper called "Model" is not spuriously
		// satisfied by an unrelated mention of "Models"/"ModelName" etc.
		// regexp.QuoteMeta guards against a future helper whose identifier
		// contains a regex metacharacter (defensive; today all identifiers
		// are [A-Za-z]).
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if !re.MatchString(rule) {
			t.Errorf(".augment/rules/07-prompts.md: template helper %q is registered in cel.BuildTemplateFuncMap but never mentioned in the rules file (add it to the 'Useful functions' catalogue in the enabledWhen Filtering & Preferred Models section, or remove it from the FuncMap)", name)
		}
	}
}

// TestRulesDocMentionsDirHelper pins the bead's headline acceptance criterion
// (mitto-eac): .augment/rules/07-prompts.md must mention the Dir(path) helper
// specifically, with the {{ Dir .Args.Test }} idiom (the whole point of the
// bead was that an agent asked to derive a sibling-file path from a filename-
// typed argument should discover Dir from the rules file).
//
// This overlaps with the FuncMap catalogue test above but is kept separate so
// the bead's acceptance criterion has a single, named guardian: if a future
// edit strips the Dir idiom while keeping the identifier elsewhere in the
// file, the catalogue test still passes but this one fails, surfacing the
// regression against the bead's original intent.
func TestRulesDocMentionsDirHelper(t *testing.T) {
	root := repoRootForTest(t)
	rule := readFileForTest(t, filepath.Join(root, ".augment", "rules", "07-prompts.md"))

	if !strings.Contains(rule, "`Dir(path)`") {
		t.Error(".augment/rules/07-prompts.md: missing `Dir(path)` entry in the useful-functions catalogue")
	}
	// The bead calls out {{ Dir .Args.Test }}/cleanup.md verbatim in its
	// proposed-solution block. Keep the idiom pinned so it stays copy-
	// pasteable for prompt authors.
	if !strings.Contains(rule, "{{ Dir .Args.Test }}") {
		t.Error(".augment/rules/07-prompts.md: missing `{{ Dir .Args.Test }}` worked-example idiom for the Dir helper")
	}
}
