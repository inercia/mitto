package prompts

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// KnownMenus is the canonical registry of real UI menu names a prompt's
// `menus:` front-matter field can target. It mirrors MENU_PARAM_TYPES in
// web/static/utils/prompts.js (frontend) exactly — see
// TestMenus_KnownMenusMatchesFrontendRegistry for the sync guard. When adding
// a new menu, update BOTH this slice AND the frontend registry.
var KnownMenus = []string{"prompts", "promptsLoop", "conversation", "beadsIssues", "beadsList"}

// MenuInternal is a dispatch-only sentinel menu token. A prompt with
// `menus: internal` matches no known UI menu and is therefore hidden from
// every surface (ChatInput dropup, loop selector, conversation/beads
// context menus). It is used throughout the builtin tree by the beads
// driver/phase prompts, which are only ever dispatched by name
// (mitto_conversation_send_prompt), never picked from a menu.
//
// It is intentionally NOT part of KnownMenus/MENU_PARAM_TYPES: it supplies no
// param types and has no frontend menu surface to satisfy.
const MenuInternal = "internal"

// KnownMenuTokens is the set of every menu token ParsePromptFile/
// ValidatePromptParameters treat as recognised: the real menus in KnownMenus
// plus the MenuInternal sentinel. A token outside this set logs a WARN via
// WarnUnknownMenus but does not fail prompt loading.
var KnownMenuTokens = func() map[string]bool {
	m := make(map[string]bool, len(KnownMenus)+1)
	for _, name := range KnownMenus {
		m[name] = true
	}
	m[MenuInternal] = true
	return m
}()

// ParseMenuTokens splits a prompt's raw comma-separated `menus` string into
// its included and excluded token lists. Blank entries are dropped and
// whitespace around each entry is trimmed. A `!`-prefixed token is routed to
// excluded with the prefix stripped (see docs/config/prompts.md#menus,
// "Exclusion Syntax"). This is the single authoritative tokenizer for the
// package — ValidatePromptParameters's childSessionId menu rule and
// UnknownMenuTokens both build on it.
func ParseMenuTokens(menus string) (included, excluded []string) {
	for _, raw := range strings.Split(menus, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "!") {
			if t := strings.TrimPrefix(token, "!"); t != "" {
				excluded = append(excluded, t)
			}
			continue
		}
		included = append(included, token)
	}
	return included, excluded
}

// UnknownMenuTokens returns the sorted, deduplicated set of tokens in menus
// (from both the included and `!`-excluded lists) that are not in
// KnownMenuTokens. Returns nil when every token is recognised.
func UnknownMenuTokens(menus string) []string {
	included, excluded := ParseMenuTokens(menus)
	seen := make(map[string]struct{})
	for _, tok := range included {
		if !KnownMenuTokens[tok] {
			seen[tok] = struct{}{}
		}
	}
	for _, tok := range excluded {
		if !KnownMenuTokens[tok] {
			seen[tok] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// menusWarnLogged provides per-process deduplication so each
// (promptName, path, tokens) combination only logs once regardless of how
// many times the prompt is reloaded (mirrors deprecationWarnLogged in
// template.go).
var menusWarnLogged sync.Map

// WarnUnknownMenus emits a single slog.Warn when menus contains one or more
// tokens not in KnownMenuTokens. A typo like `menus: conversations` (plural)
// silently dropped a prompt from its intended menu with no error anywhere in
// the pipeline (mitto-kazd); this makes that failure loud without breaking
// prompt loading — matches the non-fatal-warning convention used by
// WarnDeprecatedMittoVars. path identifies the source (a prompt file path, or
// a fixed label such as "settings"/".mittorc" for inline prompts.go where no
// real on-disk path is threaded through).
func WarnUnknownMenus(promptName, path, menus string) {
	unknown := UnknownMenuTokens(menus)
	if len(unknown) == 0 {
		return
	}
	key := promptName + "|" + path + "|" + strings.Join(unknown, ",")
	if _, loaded := menusWarnLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	slog.Warn("prompt menus field contains unrecognised token(s); prompt will not appear in the intended menu",
		"prompt", promptName,
		"path", path,
		"menus", menus,
		"unknown", unknown,
		"known", append(append([]string{}, KnownMenus...), MenuInternal))
}
