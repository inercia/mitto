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

Mitto uses a layered configuration approach for ACP servers:

1. **RC file** (`~/.mittorc`): Optional YAML config, read-only in UI, version-controllable
2. **Settings** (`MITTO_DIR/settings.json`): JSON, UI-editable, auto-created on first run

### How It Works

When both exist, ACP servers are **merged**:
- RC file servers have higher priority (override settings servers with same name)
- RC file servers are marked with `Source: "rcfile"` and cannot be edited/deleted via UI
- Settings servers are marked with `Source: "settings"` and can be managed via UI
- Users can add new servers via UI → saved to `settings.json` only

```go
// At load time:
result := LoadSettingsWithFallback()
// result.Config.ACPServers contains merged servers from both sources
// result.HasRCFileServers indicates if any servers came from RC file

// When saving (via UI):
// Only servers with Source != SourceRCFile are written to settings.json
// RC file servers remain in .mittorc (never modified by Mitto)
```

### Source Tracking

`ConfigItemSource`: `"rcfile"` (from `~/.mittorc`), `"settings"` (from `settings.json`), `"default"` (embedded). `ACPServer.Source` tracks origin; UI hides edit/delete for `rcfile` servers.

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

`folders.json` (`$MITTO_DIR`, keyed by `working_dir`) is the **authoritative store** for folder-level settings — NOT just a deduplication cache. It holds `name`, `color`, `code`, the organizational `group` label, `auto_children`, and the folder-native `beads` block. It is created the **first time via a one-time migration** that lifts inline folder fields out of `workspaces.json`; thereafter all common folder-level info always lives here. This is **transparent**: `LoadWorkspaces()` returns fully-populated `[]WorkspaceSettings`, so no other code (SessionManager, REST, frontend) is affected.

- **Load** (`LoadWorkspaces`): merges the authoritative `folders.json` via `ApplyFolderDefaults` — the folder value **wins** over any value still on a workspace (divergent legacy values collapse). Auto-migrates legacy inline files; idempotent thereafter.
- **Save** (`SaveWorkspaces`): `extractFolderSettings` hoists each folder-level field (first non-empty value across the group; divergence collapses, field stripped from every workspace), then `preserveFolderNativeFields` merges folder-native fields (`beads`) back from the existing `folders.json`. Writes `folders.json` **first**, then cleaned `workspaces.json` (crash-safe ordering). Orphan folders pruned; empty map deletes the file.
- **Folder-native fields** (`beads`): set directly via `SetFolderBeadsUpstream`/`FolderBeadsUpstream`, never via a workspace; `foldersEqual` compares them so no spurious rewrites occur.
- **`--folders FILE` flag**: loads folder settings via `LoadFoldersFromFile` (JSON/YAML, not persisted), applied via `ApplyFolderDefaults` after workspace loading. Overlays onto workspaces from any source (CLI, file, or `workspaces.json`).
- Code lives in `internal/config/folders.go`; path via `appdir.FoldersPath()` / `appdir.FoldersFileName`.
- **Metadata stays in `.mittorc`**: `description`/`url`/`group`/`user_data_schema` are version-controllable and are NOT moved to `folders.json`. NOTE: the `.mittorc` metadata `group` is a SEPARATE concept from the Mitto-local folder `group` (an organizational label hoisted into `folders.json` alongside name/color/code).

## Global Settings REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/mitto/api/config` | Get current effective config (merged RC + settings) |
| GET | `/mitto/api/settings` | Get editable settings (settings.json content) |
| POST | `/mitto/api/settings` | Save full settings (replaces settings.json) |
| POST | `/mitto/api/agents/scan` | Scan for installed ACP agents |
| POST | `/mitto/api/agents/confirm` | Confirm detected agents (adds to settings) |

Note: `/mitto/api/settings` manages global `settings.json`. For per-session feature flags, see `16-web-backend-settings.md`.

## ACP Server Constraints (Auto-Selection)

`ACPServer.Constraints` (`map[string]*ACPServerConstraint`): auto-select config options (e.g., model) when a session starts. Applied in `BackgroundSession.applyConfigConstraints()` after ACP initialization provides available options.

```json
{ "constraints": { "model": { "matchMode": "contains", "pattern": "Opus 4.6" } } }
```

`ACPServerConstraint`: `MatchMode` (`"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"`), `Pattern` (case-insensitive). `lookAlike` splits the pattern into words and checks all words appear in the name. Exposed via `GET /mitto/api/config` response.

## WorkspaceSettings Override Pattern

`WorkspaceSettings.ACPCommandOverride`: set default from server map, then apply override. See `internal/config/merger.go` for `GenericMerger[T]`.
