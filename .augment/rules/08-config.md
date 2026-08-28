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
  - ShortcutButton
  - global shortcuts
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

Workspaces persisted in `workspaces.json` (except CLI `--dir`). `folders.json` (crash-safe) holds folder-level settings; metadata stays in `.mittorc` (version-controllable). Folder-level settings include a `pinned` bool (defaults `false`) that, when `true`, keeps the folder visible in the sidebar even without conversations; toggled via `GET/PUT /api/folders/pin` (`internal/web/handlers/folder_pin.go`) and projected onto workspace records by `ApplyFolderDefaults`.

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
    tags: [Smartest, Reasoning, Expensive]            # Capability tags, consumed at runtime
```

**Tag union matching** (additive): If a model name matches multiple profiles (e.g., `Claude Opus 4.5`):
- First: Matches `Claude` profile → `[Anthropic]`
- Then: Matches `Opus` profile → Adds `[Smartest, Reasoning, Expensive]`
- Result: `[Anthropic, Smartest, Reasoning, Expensive]` (union)

Use `matchMode: contains` for robust cross-version matching. Shipped defaults include: Claude, Opus, Sonnet, Haiku, GPT-5, GPT-4, Gemini. Test: `TestParse_EmbeddedDefaultModelProfiles()` in `internal/config/config_test.go`.

**Canonical Go defaults (always available, not just first-run seed)**: `config.DefaultModelProfiles()` hardcodes the same 7 profiles in Go — the single source of truth, kept in sync with `config.default.yaml`'s `models:` block by `make check-model-tags`. `(*Config).EffectiveModelProfiles()` returns `settings.json`'s `Models` unioned with these defaults (user profile wins on name collision, defaults fill gaps; nil-safe). All tag/name resolution — `ModelProfileByName`, `ModelProfilesByTag`, `ResolveModelTags` — routes through `EffectiveModelProfiles()`, so a prompt's `preferredModels: {modelTag: Coding}` resolves even when `settings.json` predates or omits `models:` (previously it silently no-opped to the baseline model — this was the root cause of `preferredModels` "not working" for users with pre-existing settings). `config.CanonicalModelTags()` returns the sorted, de-duped tag set; `make check-model-tags` rejects any builtin prompt's `modelTag` that isn't in this set.

## ACP Server Constraints

`ACPServer.Constraints`: auto-select config options (model, etc.) on session start. MatchModes: `"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"` (word-based). Applied in `applyConfigConstraints()` after ACP init.

Prompt `preferredModels` field (see `07-prompts.md`) references these profiles by **name** (`modelName:`) or **tag** (`modelTag:`) — it does NOT use match-mode globs directly. The profile's own `criteria.matchMode` is applied indirectly, via `selectPreferredModel()`, when the resolved profile's criteria are matched against the ACP server's available models.

**Tag-based resolution is a "tier gate" (intentional)**: `SelectPreferredModel` (`internal/conversation/constraints.go` ~L83) checks the CURRENT model FIRST via `currentSatisfiesProfile`. If the current model already matches any profile carrying the requested tag, it is kept and NO `SetSessionModel` RPC is issued (avoids needless set_model at wakeup — mitto-ykb). Same semantics apply to the initial-model tier chain `WorkspaceSettings > ACPServer settings > Agent default` in `cbMaybeApplyInitialModelAsync` (`bgsession_callbacks.go`). Consequence: if the default (e.g. Claude Opus 4.7) already carries the tag (`Smartest`, `Reasoning`, `Expensive` via the built-in `Claude Opus` profile), a Workspace/Settings `modelTag: Smartest` will NOT switch to a newer variant like Opus 4.8. **To force a specific model on new-session start, reference it by `modelName:` (exact profile), not `modelTag:`**. Diagnostic checklist when a user reports "Mitto did not open on model X": (a) which tier holds the setting, (b) is it `modelName:` or `modelTag:`, (c) does the current default already satisfy the tag (tier-gate no-op), (d) does the matcher's pattern accidentally substring-match a broader set of models.

**`lookAlike` matcher uses BARE SUBSTRING, not word boundaries (bug)**: `ConstraintMatchesName` (`internal/workspaces/constraint.go` case `"lookAlike"`) splits `Pattern` on whitespace via `strings.Fields`, then per-token calls `strings.Contains(nameLower, word)` with no word-boundary guard. A profile with `{matchMode: lookAlike, pattern: "Opus 5"}` tokenizes to `["opus","5"]` and MATCHES a model named `"Opus 4.7 (500K)"` because `"5"` is contained in `"500k"`. Same trap for any short/digit token colliding with model-id suffixes or context-window labels. **Prefer `matchMode: contains` for cross-version matching**, and avoid bare-digit tokens in `lookAlike` patterns until a word-boundary fix ships. If tightening the matcher, add case-insensitive word-boundary regex semantics and pin regressions in `internal/workspaces/constraint_test.go` (e.g. `"Opus 5"` vs `"Opus 4.7 (500K)"`, `"GPT 4"` vs `"GPT 4.5"`).

Agent metadata can pre-seed these at discovery: `metadata.yaml` `defaults.constraints` (plus `defaults.env`/`tags`/`autoApprove`) map onto `ACPServer.Constraints`/`Env`/`Tags`/`AutoApprove` via `seedACPServerDefaults` (see [03-cli-acp.md](03-cli-acp.md#agent-defaults-seeded-at-discovery)). Seeding is request-wins (user-supplied values are not overwritten).

## Global Shortcuts

Global shortcut buttons (`shortcuts:` in `settings.json`) mirror per-folder shortcuts (`folders.json`) and are merged with them **at render time** on the frontend — the backend stores and serves each level independently:

- Type: `config.ShortcutButton{Icon, Prompt}` (`internal/config/folders.go`) — `Prompt` is a workspace prompt name, `Icon` an optional `PROMPT_ICONS` key.
- Sections (map key in both global and folder JSON): `conversations`, `beadsIssue`, `tasksList`.
- API: `GET/PUT /api/global/shortcuts` (`internal/web/handlers/global_shortcuts.go`) mirrors the folder shortcuts endpoint; GET also returns the available `Prompts` so the editor needs only one request.
- `buildNewSettings` must preserve `shortcuts:` so an unrelated Settings save never wipes global shortcuts.
- Defaults for new installs are seeded in embedded `config/config.default.yaml` (`shortcuts:` key) — written to `settings.json` on first run only; existing installs are untouched.

Frontend merge/dedupe logic (duplicated in `BeadsView.js` ×2 and `app.js`): global list first, then folder entries whose `prompt` isn't already in the global list. See `25-web-frontend-components.md` for the shared `ShortcutsEditor` UI component.

## Global Task Label Colors

Global task-title colors are an ordered `[]TaskLabelColor` stored as `task_label_colors` in `settings.json`. `GET/PUT /api/global/task-label-colors` owns the field; writes trim labels, normalize six-digit hex colors to lowercase, preserve unrelated settings through the raw read-modify-write path, and broadcast `task_label_colors_updated` only after a successful save. `buildNewSettings` must carry the field forward so a full Settings save cannot erase a dedicated-resource update. `BeadsView` derives the first exact label match at render time and never persists color state per task.

**Folder-scoped task label colors** mirror the global field per folder, exactly like folder shortcuts: `TaskLabelColor` lives in `internal/workspaces` (aliased back as `config.TaskLabelColor` in `workspaces_shim.go`, since `FolderSettings` is in `workspaces` and `config` imports it), stored as `task_label_colors` in `folders.json`. `FolderTaskLabelColors(workingDir)` / `SetFolderTaskLabelColors(workingDir, entries)` follow the same prune-empty (nil when `len==0`) / delete-empty-folder-entry (`folderSettingsEmpty`) semantics as `FolderShortcuts`, and the field is a folder-native field wired into `folderSettingsEmpty` and `preserveFolderNativeFields` so a workspace-driven save cannot erase it. `GET/PUT /api/folders/task-label-colors?working_dir=...` (`internal/web/handlers/folder_task_label_colors.go`) applies the global handler's entry validation plus the folder handler's `working_dir` checks (required, absolute, known workspace), and — unlike folder shortcuts, which does not broadcast — mirrors the **global** broadcast pattern, emitting `folder_task_label_colors_updated` (with `working_dir`) after a successful PUT.

## WorkspaceSettings Override Pattern

`WorkspaceSettings.ACPCommandOverride`: set default from server map, then apply override. See `internal/config/merger.go` for `GenericMerger[T]`.
