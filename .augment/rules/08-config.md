---
description: Configuration loading with LoadSettings, Config vs Settings types, queue configuration, workspace persistence, and workspace RC files
globs:
  - "internal/config/**/*"
  - "config/**/*"
keywords:
  - LoadSettings
  - LoadSettingsWithFallback
  - Config type
  - Settings type
  - mittorc
  - settings.json
  - queue configuration
  - workspace persistence
  - config merging
  - workspace RC
  - .mitto/mittorc
  - WorkspaceRC
  - SaveWorkspaceRC
  - folders.json
  - folder deduplication
  - LoadFolders
  - SaveFolders
---

# Configuration System

**Architecture docs**: See [docs/devel/workspaces.md](../docs/devel/workspaces.md) for workspace details.

## Config Layering (RC File + Settings)

**RC file** (`~/.mittorc`, version-controllable, read-only in UI) + **Settings** (`MITTO_DIR/settings.json`, UI-editable). Servers merge with RC priority (marked `Source: "rcfile"`). `LoadSettingsWithFallback()` merges both; saving only touches `settings.json` servers.

## Key Functions

| Function                     | Purpose                                                    |
| ---------------------------- | ---------------------------------------------------------- |
| `LoadSettingsWithFallback()` | Load and merge RC file + settings.json (preferred)         |
| `LoadSettings()`             | Load from `settings.json` only                             |
| `Load(path)`                 | Load from specific file (YAML or JSON)                     |
| `SaveSettings(settings)`     | Save to `settings.json`                                    |
| `MergeACPServers(rc, s)`     | Merge servers from two sources                             |
| `GetSettingsOnlyServers(s)`  | Filter to only settings-sourced servers (for saving)       |

## Config vs Settings Types

```go
// Config - internal (used in code)
type Config struct { ACPServers, Web, UI, Conversations }

// Settings - JSON format (stored in settings.json)
type Settings struct { ... }

// Conversion
settings := ConfigToSettings(cfg)
cfg := settings.ToConfig()
```

## Queue Configuration

**Important**: Queue config is **global/workspace-scoped**, NOT per-session.

```yaml
conversations:
  queue:
    enabled: true
    delay_seconds: 0
    max_size: 10
    auto_generate_titles: true
```

See [docs/devel/message-queue.md](../docs/devel/message-queue.md) for details.

## Workspace RC Files

Per-workspace config via RC files (`{workspace}/.mittorc` or `.mitto/mittorc[.yaml]`). Supports: `prompts`, `processors` (with `enabled`/`arguments`), `prompts_dirs`, `processors_dirs`, `user_data_schema`. Use `config.LoadWorkspaceRC(workingDir)` to load. See `07-prompts.md` for details.

## Workspace Persistence

Workspaces persisted in `workspaces.json` (except CLI `--dir`). `folders.json` (crash-safe) holds folder-level settings; metadata stays in `.mittorc` (version-controllable).

## Global Settings REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/mitto/api/config` | Get current effective config (merged RC + settings) |
| GET | `/mitto/api/settings` | Get editable settings (settings.json content) |
| POST | `/mitto/api/settings` | Save full settings (replaces settings.json) |
| POST | `/mitto/api/agents/scan` | Scan for installed ACP agents |
| POST | `/mitto/api/agents/confirm` | Confirm detected agents (adds to settings) |

Note: `/mitto/api/settings` manages global `settings.json`. For per-session feature flags, see `16-web-backend-settings.md`.

## Model Profiles

`models:` block in embedded `config/config.default.yaml` ships a default set of 7 profiles for **first installs only**. Existing `settings.json` is never overwritten. Pattern:

```yaml
models:
  - name: Claude Opus          # UI label (read-only)
    criteria: { matchMode: contains, pattern: Opus }  # Case-insensitive pattern matching
    tags: [Smartest, Reasoning, Expensive]            # Interface-only semantic tags
```

**Tag union matching** (additive): If a model name matches multiple profiles (e.g., `Claude Opus 4.5`):
- First: Matches `Claude` profile → `[Anthropic]`
- Then: Matches `Opus` profile → Adds `[Smartest, Reasoning, Expensive]`
- Result: `[Anthropic, Smartest, Reasoning, Expensive]` (union)

Use `matchMode: contains` for robust cross-version matching. Tags are interface-only; runtime consumption is tracked separately (see `mitto-2cc`). Shipped defaults include: Claude, Opus, Sonnet, Haiku, GPT-5, GPT-4, Gemini. Test: `TestParse_EmbeddedDefaultModelProfiles()` in `internal/config/config_test.go`.

## ACP Server Constraints

`ACPServer.Constraints`: auto-select config options (model, etc.) on session start. MatchModes: `"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"` (word-based). Applied in `applyConfigConstraints()` after ACP init.

Prompt `preferredModels` field (see `07-prompts.md`) references these profiles by **name** (`modelName:`) or **tag** (`modelTag:`) — it does NOT use match-mode globs directly. The profile's own `criteria.matchMode` is applied indirectly, via `selectPreferredModel()`, when the resolved profile's criteria are matched against the ACP server's available models.

Agent metadata can pre-seed these at discovery: `metadata.yaml` `defaults.constraints` (plus `defaults.env`/`tags`/`autoApprove`) map onto `ACPServer.Constraints`/`Env`/`Tags`/`AutoApprove` via `seedACPServerDefaults` (see [03-cli-acp.md](03-cli-acp.md#agent-defaults-seeded-at-discovery)). Seeding is request-wins (user-supplied values are not overwritten).

## WorkspaceSettings Override Pattern

`WorkspaceSettings.ACPCommandOverride`: set default from server map, then apply override. See `internal/config/merger.go` for `GenericMerger[T]`.
