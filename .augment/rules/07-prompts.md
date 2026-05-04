---
description: Prompt system architecture, workspace prompts, PromptsCache, merging priority, enable/disable mechanism, API endpoints
globs:
  - "internal/config/prompts*.go"
  - "internal/config/workspace_rc*.go"
  - "internal/web/session_api.go"
  - "web/static/app.js"
keywords:
  - prompts
  - WebPrompt
  - PromptsCache
  - MergePrompts
  - MergePromptsKeepDisabled
  - workspace-prompts
  - enabledWhen
  - enabledWhenACP
  - predefinedPrompts
  - toggle-enabled
  - prompts menu
  - .mitto/prompts
---

# Prompt System

## Architecture Overview

Prompts are predefined text snippets shown in the ChatInput "Insert predefined prompt" menu. They come from multiple sources and are merged server-side into a single list per workspace.

```
┌──────────────────────────────────────────────────────────────────────┐
│              GET /api/workspace-prompts?dir=...&session_id=...       │
│                          (Single Source of Truth)                     │
│                                                                      │
│  Priority (lowest → highest):                                        │
│  1. Global file prompts    (MITTO_DIR/prompts/*.md)                  │
│  2. Settings prompts       (settings.json .prompts)                  │
│  3. ACP server-specific    (prompts with acps: field + inline)       │
│  4. Workspace dir prompts  (.mitto/prompts/*.md)                     │
│  5. Workspace inline       (.mittorc prompts section)                │
│                                                                      │
│  Filters: enabled:false removed, enabledWhen evaluated               │
└──────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              Frontend: predefinedPrompts = workspacePrompts
              (No client-side merge — backend does everything)
```

## Prompt File Format (`.md` with YAML Frontmatter)

```markdown
---
name: "Review Code"
description: "Review code for quality"
group: "Code Quality"
backgroundColor: "#4a90d9"
enabled: true
enabledWhen: "acp.matchesServerType('augment') && tools.hasPattern('filesystem_*')"
---
Please review the following code for quality, readability, and potential bugs.
```

**Legacy shorthand fields** (`enabledWhenACP`, `enabledWhenMCP`) are still accepted for backward compatibility and are auto-translated to `enabledWhen` CEL expressions. Prefer `enabledWhen` for new prompts.

## Key Types

```go
type WebPrompt struct {
    Name            string       `json:"name"`
    Prompt          string       `json:"prompt"`
    Description     string       `json:"description,omitempty"`
    Group           string       `json:"group,omitempty"`
    BackgroundColor string       `json:"backgroundColor,omitempty"`
    Source          PromptSource `json:"source,omitempty"`    // "builtin", "file", "settings", "workspace"
    Enabled         *bool        `json:"enabled,omitempty"`   // nil = enabled, false = disabled
    EnabledWhenACP  string       `json:"-"`                   // Legacy shorthand → auto-translated to EnabledWhen
    EnabledWhenMCP  string       `json:"-"`                   // Legacy shorthand → auto-translated to EnabledWhen
    EnabledWhen     string       `json:"-"`                   // CEL expression (preferred)
}
```

## Merging Functions

```go
// Standard merge: filters out disabled prompts (enabled: false)
MergePrompts(global, settings, workspace []WebPrompt) []WebPrompt

// Keep disabled: preserves enabled:false entries (for management UI)
MergePromptsKeepDisabled(global, settings, workspace []WebPrompt) []WebPrompt
```

Higher-priority source overrides lower-priority by name. Use `MergePromptsKeepDisabled` for the `include_global=true` variant (WorkspacesDialog).

## PromptsCache (`internal/config/prompts_cache.go`)

Caches global file prompts from `MITTO_DIR/prompts/` with auto-refresh on directory changes:

```go
cache.GetWebPrompts()                      // All global prompts
cache.GetWebPromptsSpecificToACP("auggie") // Prompts with acps: "auggie"
cache.ForceReload()                        // Clear cache and reload
```

## API Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /api/workspace-prompts?dir=...&session_id=...` | Fully merged prompt list (single source of truth for menu) |
| `GET /api/workspace-prompts?dir=...&include_global=true` | All prompts including disabled (for WorkspacesDialog toggles) |
| `PUT /api/workspace-prompts/toggle-enabled` | Toggle prompt enabled/disabled state |

### Toggle-Enabled Logic

When disabling prompt X:
1. If `.mitto/prompts/X.md` exists → set `enabled: false` in frontmatter
2. If not → add `{name: X, enabled: false}` to `.mittorc` prompts section

When re-enabling:
1. If `.md` file has `enabled: false` → remove the field from frontmatter
2. If `.mittorc` has the entry → remove entry (clean up empty `prompts` key)

## Frontend Architecture (Simplified)

```javascript
// app.js — Single source of truth from backend
const [workspacePrompts, setWorkspacePrompts] = useState([]);
const predefinedPrompts = workspacePrompts; // No client-side merge!

// Refresh on: dropdown open, file watcher event, visibility change, 30s interval
const fetchWorkspacePrompts = useCallback(async (workingDir, forceRefresh) => {
  const res = await authFetch(`/api/workspace-prompts?dir=${dir}&session_id=${id}`);
  setWorkspacePrompts(data?.prompts || []);
}, [...]);
```

**Anti-pattern**: Never do client-side prompt merging — backend does everything. `predefinedPrompts = workspacePrompts` only.

## Conditional Requests

The workspace-prompts endpoint supports `If-Modified-Since` / `Last-Modified` for efficient polling (30s interval).

## enabledWhen Filtering

Server-side filtering via `filterPromptsByEnabled()` and `buildPromptEnabledContext()`.

Prefer `enabledWhen` (CEL expression). Legacy shorthands are auto-translated at load time:
- `enabledWhenACP: "augment"` → `acp.matchesServerType("augment")`
- `enabledWhenACP: "augment, claude-code"` → `acp.matchesServerType(["augment", "claude-code"])`
- `enabledWhenMCP: "mitto_*"` → `tools.hasPattern("mitto_*")` (legacy, prefer `enabledWhen`)
