---
description: Scanner defense, blocklist, IP metrics, and suspicious path detection
globs:
  - "internal/defense/**/*"
  - "internal/web/middleware_defense*.go"
keywords:
  - scanner defense
  - blocklist
  - whitelist
  - rate limit
  - suspicious path
  - IP blocking
---

# Scanner Defense (internal/defense)

Scanner defense blocks malicious IPs (vulnerability scanners, bots) at the TCP/HTTP layer when external access is enabled.

## When It Applies

- **Enabled by default** when `web.external_port` is set (external access). Can be explicitly toggled via `web.security.scanner_defense.enabled`.
- Used by `internal/web` in `middleware_defense.go`; defense is created in server setup and passed to middleware.

## Key Types

| Type | Purpose |
|------|--------|
| `ScannerDefense` | Coordinates blocklist, metrics, and background cleanup. Call `IsBlocked(ip)` (fast, O(1)) and `RecordRequest` / `RecordSuspiciousPath` from middleware. |
| `Blocklist` | In-memory blocklist with expiration, whitelist (CIDR), and optional disk persistence. |
| `IPMetrics` | Per-IP request counts and error rates within a time window. |
| `Config` | Use `defense.DefaultConfig()` then override; set `PersistPath` (e.g. from `appdir.DefenseBlocklistPath()`). |

## Conventions

- **Whitelist**: CIDR or single IP; invalid entries are logged and skipped. Localhost (`127.0.0.0/8`, `::1/128`) is in default whitelist.
- **Persistence**: Optional. Load in `New()`; save in background cleanup. Use `fileutil` for atomic writes if adding new persistence.
- **Thread safety**: All public methods are safe for concurrent use (RWMutex inside ScannerDefense, Blocklist, IPMetrics).
- **Shutdown**: Call `Stop()` on `ScannerDefense` to stop the cleanup goroutine (e.g. during server shutdown).
- **Logging**: Use `slog` with `"component", "defense"` for consistency.

## Suspicious Paths and User Agents

- `defense.SuspiciousPaths` lists paths commonly probed by scanners (e.g. `/.env`, `/.git/`, `/wp-admin`). Use `IsSuspiciousPath(path)` for matching (prefix-based, lowercase).
- Suspicious user agents (e.g. `curl/`, `python-requests`, `go-http-client`) are in `suspiciousUserAgents`; detector uses them to mark requests. Do not add generic browsers.

## Do Not

- Block before checking whitelist (ScannerDefense does this internally).
- Call `RecordRequest`/`RecordSuspiciousPath` for whitelisted IPs; use `IsWhitelisted()` when available to skip work.
- Use scanner defense for application-level auth; it is for connection-level protection only.

## References

- Config: `internal/config` (`ScannerDefenseConfig`, `WebSecurity`).
- App dir: `appdir.DefenseBlocklistPath()` for blocklist file path.
- Docs: `docs/config/` for user-facing security options.
