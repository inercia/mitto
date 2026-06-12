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

Per-workspace configuration via RC files. Search order (first found wins):
1. `{workspace}/.mittorc`
2. `{workspace}/.mitto/mittorc`
3. `{workspace}/.mitto/mittorc.yaml`

```go
// Load workspace RC
rc := config.LoadWorkspaceRC(workingDir)

// Save prompt enabled state to workspace RC
config.SaveWorkspaceRCPromptEnabled(workingDir, "Add tests", false)

// Save processor enabled state to workspace RC (mirrors prompts pattern)
config.SaveWorkspaceRCProcessorEnabled(workingDir, "memorize-preferences", true)

// Get workspace-specific overrides
dirs := sessionManager.GetWorkspacePromptsDirs(workingDir)
overrides := sessionManager.GetWorkspaceProcessorOverrides(workingDir)
```

Workspace RC supports: `prompts` (inline prompts + disable overrides), `processors` (processor enabled/disabled overrides using `{name, enabled}` entries — mirrors the prompts pattern), `prompts_dirs` (extra search paths), `processors_dirs` (extra processor search paths), `user_data_schema` (per-workspace metadata).

See `07-prompts.md` for prompt-specific workspace RC usage.

## Workspace Persistence

| Startup Mode        | Source            | Persistence      |
| ------------------- | ----------------- | ---------------- |
| CLI with `--dir`    | CLI flags         | NOT saved        |
| CLI without `--dir` | `workspaces.json` | Saved on changes |
| macOS app           | `workspaces.json` | Saved on changes |

### Folder-Level Settings (folders.json)

`folders.json` (authoritative store, keyed by `working_dir`) holds folder-level settings: `name`, `color`, `code`, `group` label, `auto_children`, folder-native `beads`. Created via one-time migration, then all common info lives here. `LoadWorkspaces` auto-migrates + merges via `ApplyFolderDefaults`. `SaveWorkspaces` extracts fields, writes `folders.json` first (crash-safe), then `workspaces.json`. Metadata (`description`/`url`/`group`/`user_data_schema`) stays in `.mittorc` (version-controllable). Code: `internal/config/folders.go`.

## Global Settings REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/mitto/api/config` | Get current effective config (merged RC + settings) |
| GET | `/mitto/api/settings` | Get editable settings (settings.json content) |
| POST | `/mitto/api/settings` | Save full settings (replaces settings.json) |
| POST | `/mitto/api/agents/scan` | Scan for installed ACP agents |
| POST | `/mitto/api/agents/confirm` | Confirm detected agents (adds to settings) |

Note: `/mitto/api/settings` manages global `settings.json`. For per-session feature flags, see `16-web-backend-settings.md`.

## ACP Server Constraints

`ACPServer.Constraints`: auto-select config options (model, etc.) on session start. MatchModes: `"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"` (word-based). Applied in `applyConfigConstraints()` after ACP init.

## WorkspaceSettings Override Pattern

`WorkspaceSettings.ACPCommandOverride`: set default from server map, then apply override. See `internal/config/merger.go` for `GenericMerger[T]`.
