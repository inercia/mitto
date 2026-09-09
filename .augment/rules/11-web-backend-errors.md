---
description: HTTP error response handling, canonical error codes, error envelope format, and migration strategy
globs:
  - "internal/web/http_helpers.go"
  - "internal/web/handlers/*.go"
keywords:
  - error handling
  - HTTP status
  - error code
  - JSON envelope
  - writeErrorJSON
---

# HTTP Error Handling & Envelope Format

## Canonical Error Codes

All HTTP errors must use one of 9 canonical error codes. Map from status → code via `defaultCodeForStatus(status)`:

| Status | Code            | Meaning                                  |
|--------|-----------------|------------------------------------------|
| 400    | `bad_request`   | Invalid request body, malformed JSON     |
| 401    | `unauthenticated` | Missing/invalid auth                     |
| 403    | `forbidden`     | Insufficient permissions                 |
| 404    | `not_found`     | Resource not found                       |
| 405    | `method_not_allowed` | Method not allowed (use `methodNotAllowed()` helper) |
| 409    | `conflict`      | State conflict (e.g., duplicate, exists) |
| 413    | `too_large`     | Request body too large                   |
| 429    | `rate_limited`  | Rate limit exceeded                      |
| 500    | `server_error`  | Server error / internal error            |

### Domain-Specific Codes

Some subsystems layer additional codes on top of the 9 canonical set. Add new ones sparingly and only when the frontend needs to branch on the specific class:

| Code                            | Status | Meaning                                                                 |
|---------------------------------|--------|-------------------------------------------------------------------------|
| `beads_schema_skew`             | 409    | bd database is behind bd binary schema and remote-backed (needs migrate) |
| `beads_migrate_publish_failed`  | 500    | `bd migrate schema` succeeded but `bd dolt push` failed                  |
| `beads_open_children`           | 409    | bd refused to close an epic that still has open child issues (mitto-phg) |

## Beads Classifier / Error-Code Convention

Any bd-CLI business-rule rejection that today falls through to a generic 500 in `writeBeadsError` (`internal/web/handlers/beads.go`) MUST be promoted via a 3-file convention (see `beads-bd-cli-business-rule-rejection-classifier-mitto-phg` memory for the full pattern):

1. **Classifier** in `internal/beads/beads.go`: `Is<Rejection>(err error) bool` matching a stable substring of bd's stderr via `strings.Contains(strings.ToLower(StderrOf(err)), "…")`. Do not parse exit codes (bd collapses on exit 1). Guard `err == nil` first.
2. **Constant** in `internal/web/handlers/helpers.go`: `errCodeBeads<X> = "beads_<x>"` next to the existing `errCodeBeadsSchemaSkew` / `errCodeBeadsMigratePublishFailed`. Point the doc-comment at `writeBeadsError`.
3. **Branch** in `writeBeadsError`: added BEFORE the terminal 5xx fallback, uses `writeJSON` directly (not `writeErrorJSON`) with an `errorEnvelope` carrying `details.stderr` when present. Log at `Warn` (nil-guarded) — the terminal branch logs at `Error`, so classifying prevents alert noise on expected rejections.

Frontend surfaces the resulting envelope via `beadsErrorFrom(err, fallback)` from `web/static/utils/sdkErrors.js` — never `errorMessage()`, which drops `details.stderr`. Test each new classifier with a `stubBeadsClient` variant in `internal/web/handlers/beads_test.go` (see `epicOpenChildrenClient` for shape) plus a `TestHandleBeads*_ReturnsActionable4xx` that pins status, `error.code`, and `error.details.stderr`.

**Anti-pattern**: substring-matching bd stderr from outside `internal/beads` — every substring test belongs on a named classifier so all callers get consistent behavior.

## Response Envelope Format

All errors are returned as JSON envelopes (no plain-text bodies):

```json
{
  "error": {
    "code": "not_found",
    "message": "Session not found"
  }
}
```

## writeErrorJSON Helper

**CRITICAL**: There are **TWO independent copies** of `writeErrorJSON`:
- `internal/web/handlers/helpers.go` — used by `package handlers` files
- `internal/web/http_helpers.go` — used by `package web` files
- **Do NOT cross-import.** Each package uses its own copy.

Use `writeErrorJSON(w, status, errorCode, message)` in all handlers. If `errorCode` is empty, the helper auto-derives the canonical code from the status:

```go
// Auto-derive code from status (404 → "not_found")
writeErrorJSON(w, http.StatusNotFound, "", "Session not found")

// Explicit code (preferred for clarity, especially non-standard statuses like 503)
writeErrorJSON(w, 503, "server_error", "Service unavailable")  // Preserves 503 status, uses canonical code

// Non-standard status codes: empty errorCode → defaults to "server_error"
writeErrorJSON(w, http.StatusServiceUnavailable, "", "msg") // 503 + server_error code

// For 405 Method Not Allowed, use the helper:
methodNotAllowed(w)  // Shorthand: 405 + method_not_allowed code
```

## Migration Strategy: Paired Backend+Frontend

When migrating from plain-text `http.Error` to JSON envelope format:

1. **Scope one handler group** (e.g., `/api/sessions/{id}/settings`)
2. **Identify all frontend consumers** of that handler group
3. **Migrate both backend + frontend in one commit** to eliminate degradation windows
4. **Use fallback parsing** on frontend: `errorData.error?.message || errorData.message || default`

Shared helpers like `parseJSONBody` have high blast radius — migrate as separate increments paired with all callers.

## Testing Error Responses

Validate envelope contract in tests using `result["error"].(map[string]interface{})["code"]` assertions.

## PATCH Toggle Pattern (Enable/Disable Resources)

When toggling the `enabled` state of a resource (prompt, processor, etc.):

**Backend** (`workspace_prompts.go` example):
```go
// PATCH /api/workspace-prompts/{name}?working_dir=/path
// Body: { "enabled": true|false }
func HandleWorkspacePromptsToggleEnabled(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    workingDir := r.URL.Query().Get("working_dir")

    var req struct{ Enabled bool `json:"enabled"` }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
        return
    }
    // ... toggle logic ...
    writeJSONOK(w, result)
}
```

**Frontend** (JavaScript):
```javascript
const response = await authFetch(
    apiUrl(`/api/workspace-prompts/${name}?working_dir=${encodeURIComponent(workingDir)}`),
    { method: "PATCH", body: JSON.stringify({ enabled: newState }) }
);
```

**Key constraints**:
- Use `PATCH` (partial update), not PUT or POST
- Name/identifier in **path** (e.g., `{name}`, not in body)
- Context param (e.g., `?working_dir=`) in **query string**
- Body contains **only** `{enabled: bool}` — no other fields
- No `/toggle-enabled` verb path; use PATCH instead

## Transient Tool Failures & Verification

When async child processes or tool calls fail transiently (e.g., `mitto_children_tasks_wait` transport error):
- Verify outcome independently from git status, working tree, and file diffs
- Check commit log and diff to confirm work actually completed
- Run quality gates (`go build`, `go vet`, tests) locally before accepting result
- Document in beads comment that the work is verified despite tool failure
