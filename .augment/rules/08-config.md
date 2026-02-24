---
description: Configuration loading with LoadSettings, Config vs Settings types, queue configuration, and workspace persistence
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

```go
type ConfigItemSource string

const (
    SourceRCFile   ConfigItemSource = "rcfile"   // From ~/.mittorc
    SourceSettings ConfigItemSource = "settings" // From settings.json
    SourceDefault  ConfigItemSource = "default"  // From embedded defaults
)

// ACPServer has Source field:
type ACPServer struct {
    Name    string
    Command string
    Source  ConfigItemSource // Track origin
}
```

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

## Workspace Persistence

| Startup Mode        | Source            | Persistence      |
| ------------------- | ----------------- | ---------------- |
| CLI with `--dir`    | CLI flags         | NOT saved        |
| CLI without `--dir` | `workspaces.json` | Saved on changes |
| macOS app           | `workspaces.json` | Saved on changes |

## Generic Merger System

The `GenericMerger[T]` type in `internal/config/merger.go` provides reusable config merging:

```go
// Create a custom merger for any config type
merger := &GenericMerger[MyType]{
    KeyFunc:   func(item MyType) string { return item.Name },
    SetSource: func(item *MyType, s ConfigItemSource) { item.Source = s },
    GetSource: func(item MyType) ConfigItemSource { return item.Source },
    Strategy:  MergeStrategyUnion, // or MergeStrategyReplace
}

result := merger.Merge(rcItems, settingsItems)
// result.Items - merged list
// result.HasRCFileItems - true if any RC file items
// result.HasSettingsItems - true if any settings items
```

Strategies:
- `MergeStrategyUnion`: Combine all, RC file overrides by key
- `MergeStrategyReplace`: Use RC file items if any, else settings
