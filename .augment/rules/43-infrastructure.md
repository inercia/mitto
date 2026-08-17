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
- `sandbox-exec` `allow_*_folders` are additive permits over a permissive base, not whitelists — see `docs/config/restricted.md#semantics-additive-permits-vs-whitelist`.

---

## Secrets (`internal/secrets`)

Process-owned, versioned credential vault. macOS stores one blob in the system
Keychain; Linux stores the same schema in a hardened file under
`MITTO_DIR/credentials`. Other platforms use `NoopStore`.

### Interface

```go
manager.Put(ref, value)
manager.Resolve(ref)
manager.Status(ref) // configured only; never returns a value
manager.Delete(ref)
```

### Package API

- `DefaultManager()` returns the cached process manager.
- `GlobalCredential`, `SlackAppCredential`, and `SlackInstallationCredential`
  construct validated typed references.
- Package-level `Put`, `Resolve`, `Status`, and `DeleteCredential` delegate to it.
- Legacy external-access/shared-token helpers resolve the vault first, then
  migrate old Keychain accounts only after backend read-back verification.
- `SetStoreForTest(NewFakeStore())` replaces both the legacy store and vault
  backend so automated tests never touch the real developer Keychain.

### Platform Storage

- Darwin: one `Mitto` / `credentials-v1` Keychain item,
  `AccessibleWhenUnlocked`, non-synchronizable. The lazy vault load is cached.
- Linux: `credentials/` must be an owner-owned mode-0700 directory and
  `vault.json` an owner-owned mode-0600 regular file. Reject symlinks and
  insecure modes; write through same-directory temp + fsync + atomic rename.
- Preserve the cached document on failed writes; mutate a copy and publish it
  only after persistence succeeds.

### Sentinel Errors

- `ErrNotFound`: credential doesn't exist.
- `ErrNotSupported`: platform not supported (NoopStore).
- `ErrCorruptVault` / `ErrUnsupportedVaultVersion`: unsafe persisted data.
- `ErrUnsafeVaultPath`: Linux ownership, mode, type, or symlink validation failed.

### Do Not

- Commit or log secret values.
- Add secret fields to status structs or API responses.
- Use generic atomic JSON helpers for the Linux vault: they create mode-0755
  parents and do not provide no-follow validation.
