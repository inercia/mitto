---
description: Per-session advanced settings (feature flags), settings API, flag registry
globs:
  - "internal/session/flags.go"
  - "internal/session/flags_test.go"
  - "internal/web/session_settings_api.go"
  - "internal/web/session_settings_api_test.go"
keywords:
  - advanced settings
  - feature flags
  - AdvancedSettings
  - can_do_introspection
  - FlagCanDoIntrospection
  - GetFlagValue
  - GetFlagDefault
  - per-session settings
  - settings API
---

# Per-Session Advanced Settings (Feature Flags)

Sessions can have individual feature flags stored in their metadata. This enables per-conversation configuration of features like MCP introspection.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Flag Registry                                 │
│  internal/session/flags.go                                       │
│  AvailableFlags: [{name, label, description, default}, ...]     │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Session Metadata                              │
│  metadata.json: { "advanced_settings": {"flag": true} }         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    REST API                                      │
│  GET  /api/advanced-flags           → List available flags       │
│  GET  /api/sessions/{id}/settings   → Get session settings       │
│  PATCH /api/sessions/{id}/settings  → Partial update settings    │
└─────────────────────────────────────────────────────────────────┘
```

## Adding New Flags

### 1. Define the Flag

```go
// internal/session/flags.go

const FlagNewFeature = "new_feature"

var AvailableFlags = []FlagDefinition{
    // ... existing flags ...
    {
        Name:        FlagNewFeature,
        Label:       "New Feature",
        Description: "Description shown in UI",
        Default:     false,  // Always default to false (opt-in)
    },
}
```

### 2. Check the Flag

```go
import "github.com/inercia/mitto/internal/session"

// Get flag value with default fallback
enabled := session.GetFlagValue(meta.AdvancedSettings, session.FlagNewFeature)

// Or check in BackgroundSession
func (bs *BackgroundSession) isFeatureEnabled() bool {
    if bs.store == nil || bs.persistedID == "" {
        return false
    }
    meta, err := bs.store.GetMetadata(bs.persistedID)
    if err != nil {
        return false
    }
    return session.GetFlagValue(meta.AdvancedSettings, session.FlagNewFeature)
}
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/advanced-flags` | List all available flags (for UI rendering) |
| GET | `/api/sessions/{id}/settings` | Get session settings (`{"settings": {}}` if none) |
| PATCH | `/api/sessions/{id}/settings` | Partial update — merges with existing |

## WebSocket Broadcast

When settings are updated, broadcast to all clients:

```go
s.BroadcastSessionSettingsUpdated(sessionID, meta.AdvancedSettings)
```

Message type: `session_settings_updated`

```json
{
  "type": "session_settings_updated",
  "data": {
    "session_id": "20260217-143052-a1b2c3d4",
    "settings": { "can_do_introspection": true }
  }
}
```

## Flag Lifecycle

1. **New session**: All flags default to `false` (AdvancedSettings is nil/empty)
2. **Enable flag**: PATCH API merges setting into existing
3. **Flag takes effect**: On next session restart (archive+unarchive)
4. **Session archived**: Settings preserved in metadata
5. **Session unarchived**: Settings restored, flags take effect


## Key Rules

- **Default to false**: All new flags must default to `false` (opt-in)
- **Use `GetFlagValue`**: Never access `AdvancedSettings["flag"]` directly — it can be nil
- **Broadcast after update**: Always call `BroadcastSessionSettingsUpdated` after PATCH
- **Flags take effect on restart**: Archive+unarchive the session after changing flags

## Related Documentation

- [MCP Servers](../../docs/devel/mcp.md) — How flags control MCP server behavior
- [Session Management](../../docs/devel/session-management.md) — Metadata structure
