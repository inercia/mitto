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

### GET /api/advanced-flags

Returns all available flags (for UI to render settings):

```json
[
  {
    "name": "can_do_introspection",
    "label": "Can do introspection",
    "description": "Allow this conversation to access Mitto's MCP server",
    "default": false
  }
]
```

### GET /api/sessions/{id}/settings

Returns current settings for a session:

```json
{
  "settings": {
    "can_do_introspection": true
  }
}
```

Returns `{"settings": {}}` if no settings exist (never null).

### PATCH /api/sessions/{id}/settings

Partial update - merges with existing settings:

```json
// Request
{ "settings": { "can_do_introspection": true } }

// Response - returns full settings after merge
{ "settings": { "can_do_introspection": true, "other_flag": false } }
```

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

## Best Practices

### ✅ Do: Default to False (Opt-In)

```go
// GOOD: All new flags default to false
{
    Name:    "new_feature",
    Default: false,  // User must explicitly enable
}
```

### ❌ Don't: Assume Settings Map Exists

```go
// BAD: Will panic if AdvancedSettings is nil
if meta.AdvancedSettings["flag"] {
    // ...
}

// GOOD: Use helper function that handles nil
if session.GetFlagValue(meta.AdvancedSettings, session.FlagName) {
    // ...
}
```

### ✅ Do: Broadcast Settings Changes

```go
// After updating settings, always broadcast
s.BroadcastSessionSettingsUpdated(sessionID, meta.AdvancedSettings)
```

## Related Documentation

- [MCP Servers](../../docs/devel/mcp.md) - How flags control MCP server behavior
- [Session Management](../../docs/devel/session-management.md) - Metadata structure

