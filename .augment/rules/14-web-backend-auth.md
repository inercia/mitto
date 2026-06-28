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
   c. Is loopback IP on internal listener? Yes → bypass auth
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
