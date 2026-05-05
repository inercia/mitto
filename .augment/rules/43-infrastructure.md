---
description: Scanner defense, blocklist, IP metrics, restricted runner, sandbox execution, secrets/keychain
globs:
  - "internal/defense/**/*"
  - "internal/web/middleware_defense*.go"
  - "internal/runner/**/*"
  - "internal/secrets/**/*"
keywords:
  - scanner defense
  - blocklist
  - rate limit
  - suspicious path
  - IP blocking
  - restricted runner
  - sandbox
  - exec
  - docker
  - variable substitution
  - secrets
  - keychain
  - credential
  - SecretStore
---

# Infrastructure Packages

## Scanner Defense (`internal/defense`)

Blocks malicious IPs at TCP/HTTP layer when external access is enabled. Enabled by default when `web.external_port` is set.

### Key Types

| Type              | Purpose                                                     |
| ----------------- | ----------------------------------------------------------- |
| `ScannerDefense`  | Coordinates blocklist, metrics, cleanup. `IsBlocked(ip)` O(1). |
| `Blocklist`       | In-memory with expiration, whitelist (CIDR), optional disk persistence. |
| `IPMetrics`       | Per-IP request counts and error rates.                      |

### Conventions

- Whitelist: CIDR or single IP. Localhost in default whitelist.
- Thread safety: All public methods safe for concurrent use (RWMutex).
- Shutdown: Call `Stop()` on `ScannerDefense` to stop cleanup goroutine.
- Don't block before checking whitelist (done internally).
- Don't use for application-level auth (connection-level only).

### Suspicious Paths/User Agents

`IsSuspiciousPath(path)` matches prefix-based (e.g., `/.env`, `/.git/`, `/wp-admin`). Suspicious user agents (`curl/`, `python-requests`) mark requests. Don't add generic browsers.

---

## Restricted Runner (`internal/runner`)

Wraps [go-restricted-runner](https://github.com/inercia/go-restricted-runner) for optional sandboxing. Default is `exec` (no restrictions).

### Config Hierarchy (highest priority last)

1. Global per-runner-type
2. Per-agent per-runner-type
3. Workspace overrides

### Variable Substitution

| Variable      | Meaning                     |
| ------------- | --------------------------- |
| `$MITTO_WORKING_DIR`  | Current workspace directory |
| `$HOME`       | User home                   |
| `$MITTO_DIR`  | Mitto data dir              |
| `$TMPDIR`     | System temp dir             |

### Fallback

When requested runner unavailable (Docker not installed, etc.), falls back to `exec`. Check `Runner.FallbackInfo` and log warning.

### Conventions

- Runners must preserve stdin/stdout for ACP JSON-RPC.
- Restricted runners can break MCP server access; prefer `exec` when MCP is used.

---

## Secrets (`internal/secrets`)

Platform-abstracted secure credential storage. macOS: system Keychain. Other: `NoopStore` (returns `ErrNotSupported`).

### Interface

```go
type SecretStore interface {
    Get(service, account string) (string, error)
    Set(service, account, password string) error
    Delete(service, account string) error
    IsSupported() bool
}
```

### Package API

- `Default()` returns platform store. Package-level `Get`, `Set`, `Delete` helpers.
- Constants: `ServiceName = "Mitto"`, `AccountExternalAccess = "external-access"`.
- Convenience: `GetExternalAccessPassword()`, `SetExternalAccessPassword()`.

### Sentinel Errors

- `ErrNotFound`: credential doesn't exist.
- `ErrNotSupported`: platform not supported (NoopStore).

### Do Not

- Commit or log secret values.
- Assume Keychain is available on non-Darwin; always handle `ErrNotSupported`.
