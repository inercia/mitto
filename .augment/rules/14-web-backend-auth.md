---
description: Authentication middleware, public paths, session management, and external access security
globs:
  - "internal/web/auth.go"
  - "internal/web/auth_*.go"
  - "internal/web/csrf.go"
  - "internal/web/csrf_test.go"
  - "web/static/auth.html"
  - "web/static/auth.js"
keywords:
  - authentication
  - auth
  - login
  - session cookie
  - public path
  - external access
  - rate limiting
  - CSRF
---

# Authentication System

## Architecture Overview

Mitto uses a dual-listener architecture for security:

| Listener     | Binding                 | Auth Required         | Use Case                     |
| ------------ | ----------------------- | --------------------- | ---------------------------- |
| **Internal** | `127.0.0.1:PORT`        | No (localhost bypass) | Local development, macOS app |
| **External** | `0.0.0.0:EXTERNAL_PORT` | Yes (always)          | Remote access, Tailscale     |

## Public Static Paths

**Critical**: Files needed by `auth.html` must be in `publicStaticPaths`:

```go
// internal/web/auth.go
var publicStaticPaths = map[string]bool{
    "/auth.html":               true,
    "/auth.js":                 true,
    "/tailwind.css":            true,  // Required for auth page styling
    "/tailwind-config-auth.js": true,
    "/styles.css":              true,
    "/styles-v2.css":           true,
    "/favicon.ico":             true,
    "/favicon.png":             true,
}
```

### ❌ Common Mistake: Missing CSS in Public Paths

**Symptom**: Login page shows unstyled HTML, browser console shows:

```
Refused to apply style from 'http://example.com/tailwind.css' because its MIME type ('text/html')
```

**Cause**: CSS file not in `publicStaticPaths` → redirected to `auth.html` → wrong MIME type

**Fix**: Add the CSS file to `publicStaticPaths` in `internal/web/auth.go`

## Public API Paths

API endpoints accessible without authentication:

```go
var publicAPIPaths = map[string]bool{
    "/api/login":             true,  // Login endpoint
    "/api/csrf-token":        true,  // CSRF token (needed before login)
    "/api/supported-runners": true,  // Platform info (no sensitive data)
}
```

## Authentication Flow

```
1. User accesses protected page
2. AuthMiddleware checks:
   a. Is auth enabled? No → pass through
   b. Is external connection? Yes → require auth
   c. Is loopback IP on internal listener? Yes → bypass auth AND CSRF (mitto-aha: known gap, not "safe by localhost")
   d. Is IP in allow list? Yes → bypass auth
   e. Is public path? Yes → pass through
   f. Has valid session cookie? Yes → pass through
   g. Otherwise → redirect to /auth.html (pages) or 401 (API)
```

## Session Management

Sessions persist to `auth_sessions.json`. Cookie: `mitto_session`, 32-byte token, 24h duration, max 10 per user (oldest evicted).

Rate limiting: 5 failed attempts → 15m lockout per IP.

## CSRF Protection

All state-changing requests require CSRF token:

```javascript
// Frontend: Get token before login
const csrfRes = await fetch("/api/csrf-token");
const { token } = await csrfRes.json();

// Include in login request
fetch("/api/login", {
  method: "POST",
  headers: { "X-CSRF-Token": token },
  body: JSON.stringify({ username, password }),
});
```

## Testing Authentication

### Disable Auth for Tests

**Critical**: Test configurations must NOT include `web.auth` section:

```yaml
# CORRECT: No auth section
web:
  host: 127.0.0.1
  port: 8089
  external_port: -1 # Disable external listener
  # NO auth section!
```

See `32-testing-playwright.md` for complete test configuration requirements.

### Testing Auth Changes

```go
func TestAuthMiddleware(t *testing.T) {
    server := &Server{
        config: Config{
            MittoConfig: &config.Config{
                Web: config.WebConfig{
                    ExternalPort: -1,  // Disabled
                },
            },
        },
        externalPort: -1,
    }
    // Test auth behavior...
}
```

## External Connections and IP Allow List

External listener requests are always authenticated, regardless of source IP. CIDR-based allow list (config `web.auth.allow.ips`) bypasses auth for trusted networks.

## Frontend Fetch Credentials Pattern

**Problem**: Raw `fetch()` with `credentials: "same-origin"` drops auth under cross-origin/proxy (Tailscale) access and never handles 401.

**Solution**: Use `authFetch(url, options?)` helper from `web/static/utils/csrf.js`:

```javascript
// Import
import { authFetch } from '../utils/index.js';

// Use
const response = await authFetch(apiUrl("/api/config"));
```

### What authFetch Does

```javascript
export async function authFetch(url, options = {}) {
  const response = await fetch(url, { ...options, credentials: "include" });
  return handleUnauthorized(response);
}
```

- Sends `credentials: "include"` (works cross-origin/Tailscale)
- Routes 401 through `handleUnauthorized` → `redirectToLogin()`
- On 401: Returns a never-resolving promise (prevents downstream execution)

### When to Use authFetch

✅ **Use authFetch** for authenticated endpoints:
- All API probes (e.g., `/api/config`, `/api/runner-defaults`, `/api/advanced-flags`)
- File operations (reads/writes in `viewer.html`)
- Session settings and external status checks

❌ **Do NOT use authFetch** for public endpoints:
- `/api/supported-runners` (explicitly in `publicAPIPaths`)
- Other endpoints explicitly listed in `publicAPIPaths`

For public endpoints, keep raw `fetch` with `credentials: "same-origin"`.

### Inline 401 Handling

Always add explicit 401 guard in critical read-path code:

```javascript
const response = await authFetch(apiUrl("/api/files"));
if (response.status === 401) {
  redirectToLogin();
  return;
}
```

This is a defensive fallback. The primary redirect happens inside `authFetch`.

## External listener startup — never silent-skip (mitto-688m)

Startup gates for the external listener live in `cmd/mitto-app/main.go` and `internal/cmd/web.go`. Both MUST route the "should we start?" decision through the pure helper `web.DecideExternalListenerStartup(externalPort, authEnabled, credErr) (start bool, reason string)` (`internal/web/external_listener_startup.go`) and, on a non-empty `reason`, surface it via `slog.Error` + operator notification (sticky native notification on macOS, stderr `⚠️ External access is DOWN: <reason>` line in the CLI). The pre-existing dynamic path `applyAuthChanges` in `internal/web/config_handlers.go` already does the right thing for saved config changes — startup paths inherited the silent-skip anti-pattern and were fixed to match. Consume the underlying credential error via the nil-safe getter `(*Server).AuthCredentialError()` (`internal/web/server_external.go`), never by dereferencing `authManager` directly. Any new "why is the external listener not starting?" reason MUST be added as a new branch on `DecideExternalListenerStartup` (with a matching case in `external_listener_startup_test.go`), never as a bespoke check at one of the two call sites — divergence between the two entrypoints is the bug shape mitto-688m closed.

## Web up-hook + health-monitor gate on actual external listener (mitto-qljn)

Companion to mitto-688m at the downstream hook layer. The up-hook launch fragment is duplicated byte-for-byte in `internal/cmd/web.go` (~L346) and `cmd/mitto-app/main.go` (~L1398): both compute `hookPort := actualPort; if actualExternalPort > 0 { hookPort = actualExternalPort }`, then previously fired `hooks.StartUp(cfg.Web.Hooks.Up, hookPort, onFailure)` unconditionally whenever `cfg.Web.Hooks.Up.Command != ""`. When `actualExternalPort <= 0` (external listener intentionally disabled, mitto-688m auth-incomplete skip, OR `StartExternalListener()` errored), the hook silently fired against the auth-exempt loopback port — a cloudflared/tailscale/ngrok tunnel templating `${PORT}` then exposed unauthenticated local traffic to the public internet (2026-09-26 incident: `port=50982` was the local random loopback port). Fix: gate the `hooks.StartUp` call on `actualExternalPort > 0` at BOTH sites; on the else branch `slog.Error("Skipping up hook: external listener is not running", ...)` — never silent-skip (mirrors mitto-688m operator visibility). The pre-existing health-monitor gate `cfg.Web.Hooks.ExternalAddress != "" && upHook != nil` becomes correct transitively because `upHook == nil` whenever the port gate fails — do NOT add a parallel `actualExternalPort > 0` check to the health-monitor gate; the transitive nil-ness IS the invariant, and a parallel check invites drift. **AUTHORING RULES**: (1) any new "hook that consumes `${PORT}`" MUST gate on `actualExternalPort > 0` — grep for `hookPort` before merging; (2) any new "external listener is up" gate MUST derive from `actualExternalPort > 0` directly OR from a symbol (like `upHook != nil`) that is itself gated on it — never from `cfg.Web.Hooks.*.Command != ""` or `ExternalAddress != ""` alone (both are configuration presence, not runtime state); (3) the dual-site discipline is the same as mitto-688m — any change to the "should we start the up hook / health monitor?" decision MUST land in BOTH files in the same commit. Regression: `TestWebSkipsUpHookWhenExternalListenerAbsent` in `internal/cmd/web_hooks_test.go` (subprocess re-exec pattern mirroring `TestWebRejectsUnauthenticatedNonLoopbackBind`); only the CLI path is unit-testable, the macOS app mirror (`//go:build darwin`+cgo/WebKit) is protected by the dual-site discipline + `go build ./...` on darwin.

## Middleware DEBUG-volume anti-pattern (mitto-1md)

The auth middleware emits 5 DEBUG records per request (`AUTH: isPublicPath: NO MATCH` / `Required for path` / `Session cookie valid` / `External connection` / `Session validated`) — once 39% of all log bytes. Same anti-pattern as closed `mitto-t3i` at a different call site. Fix: aggregate to ≤1 record per request, guard with `logger.Enabled(ctx, slog.LevelDebug)`. **Rule for any new per-request observer**: avoid DEBUG in the steady state or emit exactly one aggregated record — multi-line DEBUG traces belong behind a build tag or explicit trace flag, not the default DEBUG channel.
