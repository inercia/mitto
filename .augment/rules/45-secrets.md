---
description: Secure credential storage, Keychain (macOS), SecretStore interface
globs:
  - "internal/secrets/**/*"
keywords:
  - secrets
  - keychain
  - credential
  - password storage
  - SecretStore
---

# Secrets (internal/secrets)

Platform-abstracted secure credential storage. On macOS: system Keychain. Elsewhere: `NoopStore` (credentials stay in settings.json; operations return `ErrNotSupported`).

## Interface

```go
type SecretStore interface {
    Get(service, account string) (string, error)
    Set(service, account, password string) error
    Delete(service, account string) error
    IsSupported() bool
}
```

Implementations must be safe for concurrent use.

## Package API

- **Default store**: `Default()` returns the platform store (or NoopStore if uninitialized). Use `Get`, `Set`, `Delete` as package-level helpers.
- **Constants**: `ServiceName = "Mitto"`; account names like `AccountExternalAccess = "external-access"`.
- **Convenience**: `GetExternalAccessPassword()`, `SetExternalAccessPassword()`, `DeleteExternalAccessPassword()` use service/account constants.

## Sentinel Errors

- `ErrNotFound`: credential does not exist (Get/Delete).
- `ErrNotSupported`: store not supported on this platform (NoopStore).

Return these unwrapped so callers can use `errors.Is`.

## Platform Setup

- **Darwin**: `keychain_darwin.go` sets package-level `store` in `init()`. Build-tagged.
- **Other**: `noop.go` provides `NoopStore`; ensure `store` is set or `Default()` falls back to NoopStore.
- Callers should check `IsSupported()` before prompting to store credentials in keychain; if false, keep in settings (or document that they remain in settings).

## Do Not

- Commit or log actual secret values.
- Add new account names without a constant and, if user-facing, documentation.
- Assume Keychain is available on non-Darwin; always handle `ErrNotSupported`.
