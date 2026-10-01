---
description: Config-option reconciliation after a model change (reasoning effort, thought_level, etc.) — policy, adjustments array, and Copilot CLI investigation notes
globs:
  - "internal/conversation/config_manager.go"
  - "internal/conversation/bgsession_callbacks.go"
  - "internal/conversation/config_reconcile_test.go"
  - "internal/acpproc/shared_acp_process.go"
keywords:
  - reasoning effort
  - thought_level
  - config reconciliation
  - ConfigAdjustment
  - adjustments
  - session/set_model
  - session/set_config_option
  - snapshot_unconfirmed
  - option_removed
  - value_not_advertised
  - mitto-bnk
---

# Config Reconciliation After a Model Change (mitto-bnk)

When the active model changes, any user-set "dependent" config options (e.g.
`reasoning_effort` / `thought_level`) may no longer be valid for the new
model's advertised option snapshot. `internal/conversation/config_manager.go`
reconciles this **portably** — no agent-specific special-casing.

## Policy (implemented in commits 9c7b9491, c55e0911)

1. Model changes are always applied **before** dependent options
   (`setModelAndReconcile` → `reconcileDependentConfigAfterModelChange`).
2. After `session/set_model` (or legacy `session/set_config_option` fallback
   on JSON-RPC `-32601`), Mitto waits up to **5s**
   (`configSnapshotWaitTimeout`) for a fresh config-option snapshot tied to
   the new model generation (`cmWaitConfigSnapshotAfter`).
3. Pending/overridden option IDs are replayed against the **new** snapshot
   (`applyPreferencesToSnapshot`):
   - Option no longer advertised at all → dropped, reason `option_removed`.
   - Option advertised but the specific value isn't in its enum → dropped
     (if durable) or kept as agent's `currentValue`, reason
     `value_not_advertised`.
   - Snapshot never arrived within 5s → reason `snapshot_unconfirmed` (one
     adjustment per pending ID), original overrides preserved for later retry.
   - Value already matches current → no-op, no adjustment emitted.
4. **No universal "unset"/"reset" token exists in ACP.** Never invent one
   (e.g. `""`, `"none"`, `"default"`) as a generic reset RPC value — only
   drop the override locally and let the agent's own `currentValue` stand.
5. Startup/resume (`revalidateOverrides`) silently drops unfindable
   overrides from the in-memory map only (storage untouched) when
   `keepUnadvertised=true` — no adjustment is emitted on cold resume.

## `adjustments` wire field

`config_option_changed` broadcasts (session_manager.go
`broadcastConfigOptionChanged` path only — NOT the per-session legacy
`OnConfigOptionChanged` mirror or REST path) carry an optional `adjustments`
array, present only when non-empty:

```go
type ConfigAdjustment struct {
    ConfigID       string `json:"config_id"`
    RequestedValue string `json:"requested_value,omitempty"`
    AppliedValue   string `json:"applied_value,omitempty"`
    Reason         string `json:"reason"`
}
```

Reasons: `option_removed`, `value_not_advertised`, `snapshot_unconfirmed`.
Full wire documentation: `docs/devel/websockets/protocol-spec.md`
`config_option_changed` section. Full policy writeup:
`docs/devel/acp.md` § "Reconciling Dependent Config After a Model Change".

## Copilot CLI 1.0.90 investigation (mitto-bnk, dependency mitto-3wd)

Observed via Copilot CLI directly (not Mitto): switching Sonnet 5 → Haiku
removes `thought_level` from the option snapshot; setting
`reasoning_effort=high` while on Haiku still returns success with no option
present; switching back to Sonnet reports `high` again — the **provider**
retains the preference across model switches, not just Mitto's local state.

**What is proven**: the retention behavior above, and that
`session/set_model` returns `{}` plus a `config_option_update` notification.

**What is NOT proven**: that this retained preference actually causes an
inference-time error when prompting Haiku. An isolated ACP harness was built
(sandboxed `COPILOT_HOME`, zero real credential exposure, outside the repo)
and verified against the real `copilot --acp` binary: unauthenticated
`initialize` succeeds, but `session/new` correctly fails with a structured
JSON-RPC `-32603` / `403 unauthorized: not authorized to use this Copilot
feature` (`category: permission_denied`). Reproducing the actual inference
path requires a real `copilot login` session, which was deliberately not
attempted. **Do not claim the Haiku inference failure was reproduced** — it
was not. Treat it as an open, credible-but-unconfirmed report until someone
with an authenticated Copilot CLI session reproduces it end-to-end.

If a future investigation DOES confirm a retained-effort defect server-side
on Copilot's end, prefer a narrow, capability-gated adapter shim scoped to
the Copilot agent type (not a generic ACP-wide workaround) or an upstream bug
report — do not build speculative workarounds without reproduction evidence.
