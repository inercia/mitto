---
description: Configuration loading, settings persistence, and workspace management
globs:
  - "internal/config/**/*"
  - "config/**/*"
  - "**/*.yaml"
  - "**/*.json"
---

# Configuration System

**Architecture docs**: See [docs/devel/workspaces.md](../docs/devel/workspaces.md) for workspace details.

## Two-Tier System

1. **Default config** (`config/config.default.yaml`): Embedded, bootstraps `settings.json`
2. **User settings** (`MITTO_DIR/settings.json`): JSON, auto-created on first run

## Key Functions

| Function | Purpose |
|----------|---------|
| `LoadSettings()` | Load from `settings.json`, create from defaults if missing |
| `Load(path)` | Load from specific file (YAML or JSON) |
| `SaveSettings(settings)` | Save to `settings.json` |
| `ConfigToSettings(cfg)` | Convert Config → Settings for JSON |

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

| Startup Mode | Source | Persistence |
|--------------|--------|-------------|
| CLI with `--dir` | CLI flags | NOT saved |
| CLI without `--dir` | `workspaces.json` | Saved on changes |
| macOS app | `workspaces.json` | Saved on changes |

