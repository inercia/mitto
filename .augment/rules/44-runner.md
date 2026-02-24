---
description: Restricted runner, sandbox execution, go-restricted-runner, variable substitution
globs:
  - "internal/runner/**/*"
keywords:
  - restricted runner
  - sandbox
  - exec
  - docker
  - sandbox-exec
  - firejail
  - variable substitution
  - WORKSPACE
  - MITTO_DIR
---

# Restricted Runner (internal/runner)

The runner package wraps [go-restricted-runner](https://github.com/inercia/go-restricted-runner) to run ACP agents with optional sandboxing. **Default is `exec` (no restrictions)**; users opt-in to sandboxing via config.

## Configuration Hierarchy (highest priority last)

1. Global per-runner-type config
2. Per-agent per-runner-type config
3. Workspace overrides for the resolved runner type

Use `NewRunner(globalRunnersByType, agentRunnersByType, workspaceConfigByType, workspace, logger)` to resolve and create a runner. If all configs are nil, you get an exec runner.

## Variable Substitution

Paths in restrictions support variables; resolved at runner creation time:

| Variable | Meaning |
|----------|---------|
| `$WORKSPACE` / `${WORKSPACE}` | Current workspace directory |
| `$HOME` / `${HOME}` | User home |
| `$MITTO_DIR` / `${MITTO_DIR}` | Mitto data dir (`appdir.Dir()`) |
| `$USER` / `${USER}` | Username (or `USERNAME` on Windows) |
| `$TMPDIR` / `${TMPDIR}` | System temp dir |

Use `VariableResolver` and `Resolve(path)` for custom substitution. Also supports `~` for home.

## Fallback to Exec

When the requested runner type is unavailable (e.g. Docker not installed, unsupported platform), the package falls back to `exec`. Check `Runner.FallbackInfo` after `NewRunner`:

- `RequestedType`: what was requested
- `FallbackType`: what was used (usually `"exec"`)
- `Reason`: error message

Log a warning when `FallbackInfo != nil`.

## Conventions

- **ACP / stdio**: Runners must preserve stdin/stdout for JSON-RPC; no network ports for ACP.
- **MCP**: Restricted runners can break MCP server access (network/fs). Document this; prefer `exec` or careful allowlists when MCP is used.
- **Config types**: Use `config.WorkspaceRunnerConfig`, `config.RunnerRestrictions`; convert to go-restricted-runner options inside the package only.
- **Testing**: Use `runner/integration_test.go` and `runner/sandbox_integration_test.go` for integration tests; unit test variable resolution and config resolution.

## References

- User config: `docs/config/restricted.md`
- Integration plan: `docs/devel/restricted-runner-integration.md`
- Config types: `internal/config` (runner-related structs)
