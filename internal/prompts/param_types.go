package prompts

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// KnownPromptParameterTypes is the canonical registry of supported parameter types
// for the structured `parameters:` field in .prompt.yaml files.
//
// This slice is the SINGLE SOURCE OF TRUTH for backend type validation.
// It is mirrored by KNOWN_PARAM_TYPES in web/static/utils/prompts.js (frontend)
// and surfaced via MCP tool schemas (sibling bead .2). When adding a new type,
// update BOTH this slice AND the frontend mirror — they must stay in sync.
//
// Type semantics:
//   - beadsId        — a beads issue ID (e.g. "mitto-42")
//   - beadsTitle     — a beads issue title (free text, typically auto-filled)
//   - sessionId      — a Mitto conversation/session UUID
//   - childSessionId — a child conversation/session UUID (relative to the host conversation)
//   - workspaceId    — a Mitto workspace UUID
//   - workspaceFolder — an absolute path to the workspace root directory
//   - acpServer      — an ACP server (agent) name
//   - text           — generic free-form text (the catch-all type)
//   - boolean        — a yes/no flag, rendered as a checkbox; supplied as the
//     string "true" or "false". Boolean parameters never gate
//     menu visibility and are always collected via the dialog
//     (a checkbox always has a definite answer; default false).
//   - prompts        — the NAME of another workspace prompt, rendered as a picker
//     in the parameter dialog. Interactive, dialog-collected (like
//     boolean): no menu auto-supplies it and it never gates menu
//     visibility. Feeds the {{ PromptText .Args.NAME }} template
//     action. multiLine is not supported (rejected by the existing
//     text-only check). The picked prompt's own parameters are, by
//     default, collected in a nested sub-dialog and shipped as a
//     `<Name>_Args` companion argument; set `collectInnerArgs: false`
//     to opt out when the picker is used only as a name/edit-subject
//     reference and the picked prompt's own parameter values are never
//     consumed (mitto-48c). collectInnerArgs is only valid on type
//     "prompts" (rejected elsewhere).
//   - filename       — a workspace-relative file path, rendered as a dropdown
//     of files under an optional Dir (workspace-relative, non-recursive),
//     optionally filtered by a Glob (filepath.Match). Interactive,
//     dialog-collected (like boolean/prompts): no menu auto-supplies it and
//     it never gates menu visibility. Feeds the {{ ReadFile .Args.NAME }}
//     template action, which enforces path safety (absolute-path reject,
//     ".." reject, symlink-escape reject, 256 KB cap) at read time — Dir/Glob
//     are UI dropdown hints only. multiLine/options are not supported
//     (rejected by the existing text-only check).
//   - dirname        — a workspace-relative directory path, rendered as a
//     dropdown of immediate sub-directories under an optional Dir
//     (workspace-relative, non-recursive), optionally filtered by a Glob
//     (filepath.Match applied to the sub-directory name only). Interactive,
//     dialog-collected (like boolean/prompts/filename): no menu auto-supplies
//     it and it never gates menu visibility. Hidden directories (leading ".")
//     are excluded by default. Returned value is a workspace-relative
//     directory path suitable for the {{ dirExists .Args.NAME }} template
//     action or joining with a filename to build a ReadFile argument. Dir/Glob
//     are UI dropdown hints only — path safety for downstream consumers is
//     enforced at read time by the helper that consumes the value.
//     multiLine/options are not supported (rejected by the existing text-only
//     check).
var KnownPromptParameterTypes = []string{
	"beadsId",
	"beadsTitle",
	"sessionId",
	"childSessionId",
	"workspaceId",
	"workspaceFolder",
	"acpServer",
	"text",
	"boolean",
	"prompts",
	"filename",
	"dirname",
}

// IsKnownPromptParameterType reports whether t is a recognised parameter type.
func IsKnownPromptParameterType(t string) bool {
	for _, known := range KnownPromptParameterTypes {
		if t == known {
			return true
		}
	}
	return false
}

// Remember* constants enumerate the accepted values of PromptParameter.Remember.
// An empty string is treated as RememberNever (default: do not persist).
//
//   - RememberNever: do not persist (default)
//   - RememberFolder: per-workspace persistence, keyed by workspace UUID
//   - RememberConversation: per-session persistence, keyed by session ID
//     (mitto-47y.6.2). Applies at both outer and inner (`type: prompts`)
//     picker scopes with the same read/write semantics as folder.
//   - RememberGlobal: reserved; accepted by the enum but not stored in v1
const (
	RememberNever        = "never"
	RememberFolder       = "folder"
	RememberConversation = "conversation"
	RememberGlobal       = "global"
)

// IsValidRemember reports whether s is an accepted value for the Remember
// field of a PromptParameter. An empty string counts as valid (means "never").
func IsValidRemember(s string) bool {
	switch s {
	case "", RememberNever, RememberFolder, RememberConversation, RememberGlobal:
		return true
	}
	return false
}

// Ask* constants enumerate the accepted values of PromptParameter.Ask.
// An empty string is treated as AskAuto (default).
//
//   - AskAuto: render in the parameter dialog only when the parameter is
//     required or is an interactive picker (default)
//   - AskAlways: always render in the parameter dialog once it opens, even for
//     an optional free-text parameter. Still non-blocking when optional.
const (
	AskAuto   = "auto"
	AskAlways = "always"
)

// IsValidAsk reports whether s is an accepted value for the Ask field of a
// PromptParameter. An empty string counts as valid (means "auto").
func IsValidAsk(s string) bool {
	switch s {
	case "", AskAuto, AskAlways:
		return true
	}
	return false
}

// KnownPromptCacheDestinations is the registry of valid cache destination values
// for the PromptParameterCache.Destination field. Only "memory" is valid in v1;
// additional destinations (e.g. "disk") may be added in future versions.
var KnownPromptCacheDestinations = map[string]bool{
	"memory": true,
}

// ParsedTTL parses the TTL field of a PromptParameterCache.
// An empty TTL means "no expiry / conversation lifetime" and returns (0, nil).
// A non-empty TTL must be a valid Go duration string with a positive value;
// otherwise an error is returned.
func (c *PromptParameterCache) ParsedTTL() (time.Duration, error) {
	if c.TTL == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.TTL)
	if err != nil {
		return 0, fmt.Errorf("invalid cache ttl %q: %w", c.TTL, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid cache ttl %q: must be a positive duration", c.TTL)
	}
	return d, nil
}

// ValidatePromptParameters validates a prompt's declared parameters against the
// known type registry and any type-specific menu constraints.
//   - menus is the prompt's raw comma-separated menus string ("" => treated as "prompts").
//   - childSessionId parameters are only valid in prompts targeting the
//     "prompts" and/or "conversation" menus.
//   - Cache blocks (when present) must have a known destination and a valid TTL.
func ValidatePromptParameters(menus string, params []PromptParameter) error {
	for i, param := range params {
		if param.Name == "" {
			return fmt.Errorf("parameter #%d: name must not be empty", i+1)
		}
		if param.Type == "" || !IsKnownPromptParameterType(param.Type) {
			return fmt.Errorf("parameter %q has unknown type %q (must be one of: %s)", param.Name, param.Type, strings.Join(KnownPromptParameterTypes, ", "))
		}
		// multiLine only controls how a free-text field is rendered, so it is
		// only meaningful for the "text" type. Reject it elsewhere to catch
		// misconfiguration early.
		if param.MultiLine && param.Type != "text" {
			return fmt.Errorf("parameter %q: multiLine is only valid for type \"text\", not %q", param.Name, param.Type)
		}
		// options constrains a "text" parameter to a fixed enumeration rendered
		// as a dropdown. It is only meaningful on type "text", mutually exclusive
		// with multiLine, and must contain non-empty, unique values. When a
		// default is declared it must be one of the listed options.
		if len(param.Options) > 0 {
			if param.Type != "text" {
				return fmt.Errorf("parameter %q: options is only valid for type \"text\", not %q", param.Name, param.Type)
			}
			if param.MultiLine {
				return fmt.Errorf("parameter %q: options and multiLine are mutually exclusive (dropdown vs. textarea)", param.Name)
			}
			seen := make(map[string]struct{}, len(param.Options))
			for _, opt := range param.Options {
				if opt == "" {
					return fmt.Errorf("parameter %q: options must not contain empty strings", param.Name)
				}
				if _, dup := seen[opt]; dup {
					return fmt.Errorf("parameter %q: options must not contain duplicate values (%q)", param.Name, opt)
				}
				seen[opt] = struct{}{}
			}
			if param.Default != "" {
				if _, ok := seen[param.Default]; !ok {
					return fmt.Errorf("parameter %q: default %q is not one of the declared options", param.Name, param.Default)
				}
			}
		}
		// Dir/Glob are only meaningful for the "filename" and "dirname" types.
		// Reject elsewhere to catch misconfiguration early (mirrors the
		// multiLine/options pattern).
		isFileOrDirType := param.Type == "filename" || param.Type == "dirname"
		if param.Dir != "" && !isFileOrDirType {
			return fmt.Errorf("parameter %q: dir is only valid for types \"filename\" or \"dirname\", not %q", param.Name, param.Type)
		}
		if len(param.Glob) > 0 && !isFileOrDirType {
			return fmt.Errorf("parameter %q: glob is only valid for types \"filename\" or \"dirname\", not %q", param.Name, param.Type)
		}
		// collectInnerArgs only controls nested-args collection for a
		// "prompts" picker's own sub-dialog; reject it elsewhere (mirrors
		// the multiLine/options/dir/glob "only valid for type X" pattern).
		if param.CollectInnerArgs != nil && param.Type != "prompts" {
			return fmt.Errorf("parameter %q: collectInnerArgs is only valid for type \"prompts\", not %q", param.Name, param.Type)
		}
		if isFileOrDirType {
			// Dir must be workspace-relative: no absolute paths, no ".." segments.
			// The runtime endpoint re-checks containment against the workspace
			// root; these guards catch obvious misconfiguration at load time.
			if param.Dir != "" {
				if filepath.IsAbs(param.Dir) {
					return fmt.Errorf("parameter %q: dir must be workspace-relative, not absolute (%q)", param.Name, param.Dir)
				}
				clean := filepath.Clean(param.Dir)
				if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					return fmt.Errorf("parameter %q: dir must not escape the workspace root (%q)", param.Name, param.Dir)
				}
				for _, seg := range strings.Split(param.Dir, string(filepath.Separator)) {
					if seg == ".." {
						return fmt.Errorf("parameter %q: dir must not contain %q segments (%q)", param.Name, "..", param.Dir)
					}
				}
			}
			// Glob compile-check: reject malformed patterns at load time so
			// prompt files fail-fast instead of at first UI open. Uses
			// doublestar so patterns with "**" (recursive) are accepted here
			// and honored by the runtime endpoint. Every entry in the list
			// is validated; an empty entry is rejected explicitly (an empty
			// pattern would silently match nothing).
			for _, g := range param.Glob {
				if g == "" {
					return fmt.Errorf("parameter %q: glob entries must not be empty", param.Name)
				}
				if !doublestar.ValidatePattern(g) {
					return fmt.Errorf("parameter %q: invalid glob %q", param.Name, g)
				}
			}
		}
		// Validate the optional Remember field: reject unknown values so
		// prompt files fail-fast instead of silently ignoring a typo (mitto-x8v).
		if !IsValidRemember(param.Remember) {
			return fmt.Errorf("parameter %q: unknown remember value %q (must be one of: %q, %q, %q, %q)",
				param.Name, param.Remember, RememberNever, RememberFolder, RememberConversation, RememberGlobal)
		}
		// Validate the optional Ask field: reject unknown values so a typo
		// fails fast instead of silently degrading to the default behaviour.
		if !IsValidAsk(param.Ask) {
			return fmt.Errorf("parameter %q: unknown ask value %q (must be one of: %q, %q)",
				param.Name, param.Ask, AskAuto, AskAlways)
		}
		// Validate the optional cache block.
		if param.Cache != nil {
			if !KnownPromptCacheDestinations[param.Cache.Destination] {
				known := make([]string, 0, len(KnownPromptCacheDestinations))
				for k := range KnownPromptCacheDestinations {
					known = append(known, k)
				}
				return fmt.Errorf("parameter %q: cache destination %q is not valid (must be one of: %s)", param.Name, param.Cache.Destination, strings.Join(known, ", "))
			}
			if _, err := param.Cache.ParsedTTL(); err != nil {
				return fmt.Errorf("parameter %q: %w", param.Name, err)
			}
		}
	}
	// childSessionId menu rule: only valid in "prompts" and/or "conversation" menus.
	for _, param := range params {
		if param.Type != "childSessionId" {
			continue
		}
		parts := strings.Split(menus, ",")
		var menuList []string
		for _, m := range parts {
			if m = strings.TrimSpace(m); m != "" && !strings.HasPrefix(m, "!") {
				menuList = append(menuList, m)
			}
		}
		if len(menuList) == 0 {
			// Empty menus treated as "prompts" — allowed.
			return nil
		}
		for _, m := range menuList {
			if m != "prompts" && m != "conversation" {
				return fmt.Errorf("parameter %q of type childSessionId is only valid in prompts targeting the 'prompts' or 'conversation' menus, but this prompt targets '%s'", param.Name, m)
			}
		}
		return nil // valid menu set; no need to re-check for additional childSessionId params
	}
	return nil
}
