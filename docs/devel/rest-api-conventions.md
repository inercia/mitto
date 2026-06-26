# REST API Conventions

This document defines the canonical conventions for Mitto's `internal/web` HTTP REST API and provides a complete **current → target** endpoint mapping. It is the keystone decision for the `mitto-ank` REST API coherence epic.

---

## 1. Resource Hierarchy

Resources are nested under their parent. Workspaces are identified by `{uuid}` (the workspace UUID), not by a `?dir=` / `?working_dir=` query-param:

```
/api/workspaces/{uuid}/...
/api/sessions/{id}/...
```

Where a query param is still needed for workspace context outside the hierarchy (e.g., global prompts listing before a workspace UUID is known), use `working_dir` as the canonical param name — **not** `dir`. Audit note: `?dir=` currently appears in several workspace-prompt endpoints and must be migrated to `?working_dir=`.

---

## 2. Path Naming

- Lowercase, plural collection nouns: `/sessions`, `/workspaces`, `/issues`, `/prompts`, `/processors`, `/images`, `/files`.
- Hyphen-separated multi-word resource names: `/mcp-tools`, `/run-now`, `/user-data`.
- No verb-style path segments except for documented **action sub-paths** (see §5).
- Single param name for workspace directory context: **`working_dir`** everywhere.

---

## 3. HTTP Methods

| Intention                         | Method   |
| --------------------------------- | -------- |
| Read a resource or collection     | `GET`    |
| Create a new resource             | `POST`   |
| Full replace of a resource        | `PUT`    |
| Partial update of a resource      | `PATCH`  |
| Remove a resource                 | `DELETE` |
| Enable / disable a resource       | `PATCH`  |

**Enable/disable** a prompt or processor → `PATCH` the resource (body: `{ "enabled": true/false }`). The `/toggle-enabled` action path is eliminated.

### Action sub-paths (acceptable non-CRUD paths)

Some operations are genuinely non-CRUD and do not map cleanly to a resource:

| Path pattern                                | Reason acceptable                              |
| ------------------------------------------- | ---------------------------------------------- |
| `POST .../periodic/run-now`                 | One-shot trigger, not a resource mutation      |
| `POST .../queue/{id}/move`                  | Reorder within queue, no natural PATCH target  |
| `POST .../sessions/{id}/prune`              | Destructive bulk operation on opaque internals |
| `POST /api/agents/scan`                     | Long-running discovery action                  |
| `POST /api/agents/confirm`                  | Confirmation step in two-phase flow            |
| `POST /api/workspaces/{uuid}/mcp-tools/install` | Package-install side-effect                |
| `POST /api/workspaces/{uuid}/mcp-tools/remove` | Package-remove side-effect                 |

---

## 4. Error Envelope

Every non-exception API response that indicates an error MUST use this JSON shape:

```json
{
  "error": {
    "code": "not_found",
    "message": "Session 20260101-120000-abc not found.",
    "details": { }
  }
}
```

`details` is optional and may carry structured context (field name, constraint, etc.).

Endpoints in the `external-stable` exception list (§6) may retain a legacy flat
`{ "error": "code", "message": "..." }` error shape where external callers depend
on it — e.g. `POST /api/callback/{token}` (emitted via a dedicated `writeCallbackError`).

### HTTP Status → error code table

| HTTP Status | `error.code`        | When to use                                     |
| ----------- | ------------------- | ----------------------------------------------- |
| 400         | `bad_request`       | Malformed input, missing required field         |
| 401         | `unauthenticated`   | No valid session / token                        |
| 403         | `forbidden`         | Authenticated but lacks permission              |
| 404         | `not_found`         | Resource does not exist                         |
| 405         | `method_not_allowed`| HTTP method not supported on this path          |
| 409         | `conflict`          | State conflict (e.g., session already running)  |
| 413         | `too_large`         | Payload exceeds size limit                      |
| 429         | `rate_limited`      | Too many requests                               |
| 500         | `server_error`      | Unexpected internal error                       |

---

## 5. Method-Not-Allowed (405) Handling

The Go 1.22+ `net/http.ServeMux` returns 405 automatically when a method-specific route pattern (`METHOD /path`) does not match. Mitto currently uses catch-all `HandleFunc` patterns and dispatches methods manually. The migration target registers routes with explicit method prefixes so 405 is uniform and requires no per-handler boilerplate.

---

## 6. Exception List — `external-stable`

These endpoints are called by external callers (native macOS app, load balancers, viewer pages) and **must not be renamed**:

| Path                                          | Caller / reason                               |
| --------------------------------------------- | --------------------------------------------- |
| `POST /api/callback/{token}`                  | External HTTP webhook callers via public URL  |
| `GET /api/health`                             | Load balancer / monitoring probes             |
| `POST /api/login`, `POST /api/logout`         | Auth system; form/redirect-based              |
| `GET /api/auth-info`                          | Login page UI adaptation                      |
| `GET /api/csrf-token`                         | Frontend CSRF bootstrap on page load          |
| `GET /api/files`                              | Viewer page served to browser tabs            |
| `POST /api/save-file-to-path`                 | Native macOS app (localhost only)             |
| `GET /api/check-file-exists`                  | Native macOS app (localhost only)             |
| `POST /api/sessions/{id}/images/from-path`    | Native macOS app image paste (localhost only) |
| `POST /api/sessions/{id}/files/from-path`     | Native macOS app file attach (localhost only) |
| `POST /api/badge-click`                       | Native macOS app Dock badge click             |

---

## 7. Current → Target Endpoint Mapping

Legend: **migrate** = path/method change needed · **keep** = stays as-is · **external-stable** = must not change.

### 7.1 Sessions

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/sessions` | GET | `/api/sessions` | GET | keep | Correct already |
| `/api/sessions` | POST | `/api/sessions` | POST | keep | Correct already |
| `/api/sessions/running` | GET | `/api/sessions/running` | GET | keep | Useful filter sub-path |
| `/api/sessions/{id}` | GET | `/api/sessions/{id}` | GET | keep | Correct already |
| `/api/sessions/{id}` | PATCH | `/api/sessions/{id}` | PATCH | keep | Correct already |
| `/api/sessions/{id}` | DELETE | `/api/sessions/{id}` | DELETE | keep | Correct already |
| `/api/sessions/{id}/events` | GET | `/api/sessions/{id}/events` | GET | keep | Correct already |
| `/api/sessions/{id}/ws` | WS | `/api/sessions/{id}/ws` | WS | keep | WebSocket; correct |
| `/api/sessions/{id}/images` | GET, POST | `/api/sessions/{id}/images` | GET, POST | keep | Correct already |
| `/api/sessions/{id}/images/{imageId}` | GET, DELETE | `/api/sessions/{id}/images/{imageId}` | GET, DELETE | keep | Correct already |
| `/api/sessions/{id}/images/from-path` | POST | `/api/sessions/{id}/images/from-path` | POST | external-stable | Native macOS app; localhost-only |
| `/api/sessions/{id}/files` | GET, POST | `/api/sessions/{id}/files` | GET, POST | keep | Correct already |
| `/api/sessions/{id}/files/{fileId}` | GET, DELETE | `/api/sessions/{id}/files/{fileId}` | GET, DELETE | keep | Correct already |
| `/api/sessions/{id}/files/from-path` | POST | `/api/sessions/{id}/files/from-path` | POST | external-stable | Native macOS app; localhost-only |
| `/api/sessions/{id}/queue` | GET, POST, DELETE | `/api/sessions/{id}/queue` | GET, POST, DELETE | keep | Correct already |
| `/api/sessions/{id}/queue/{msgId}` | GET, DELETE | `/api/sessions/{id}/queue/{msgId}` | GET, DELETE | keep | Correct already |
| `/api/sessions/{id}/queue/{msgId}/move` | POST | `/api/sessions/{id}/queue/{msgId}/move` | POST | keep | Non-CRUD action; acceptable |
| `/api/sessions/{id}/user-data` | GET, PUT | `/api/sessions/{id}/user-data` | GET, PUT | keep | Correct already |
| `/api/sessions/{id}/periodic` | GET, PUT, PATCH, DELETE | `/api/sessions/{id}/periodic` | GET, PUT, PATCH, DELETE | keep | Correct already |
| `/api/sessions/{id}/periodic/run-now` | POST | `/api/sessions/{id}/periodic/run-now` | POST | keep | Non-CRUD action; acceptable |
| `/api/sessions/{id}/callback` | GET, POST, DELETE | `/api/sessions/{id}/callback` | GET, POST, DELETE | keep | Correct already |
| `/api/sessions/{id}/settings` | GET, PATCH | `/api/sessions/{id}/settings` | GET, PATCH | keep | Correct already |
| `/api/sessions/{id}/prune` | POST | `/api/sessions/{id}/prune` | POST | keep | Non-CRUD bulk action; acceptable |
| `/api/sessions/{id}/changes` | GET | `/api/sessions/{id}/changes` | GET | keep | Correct already |

### 7.2 Workspaces

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/workspaces` | GET | `/api/workspaces` | GET | keep | Correct already |
| `/api/workspaces` | POST | `/api/workspaces` | POST | keep | Correct already |
| `/api/workspaces` | DELETE | `/api/workspaces` | DELETE | keep | Correct (workspace is identified by `?working_dir=`) |
| `/api/workspaces/` | GET | `/api/workspaces/{uuid}` | GET | migrate | Replace query-param lookup with UUID path segment |
| `/api/workspace-prompts` | GET, POST, DELETE | `/api/workspaces/{uuid}/prompts` | GET, POST, DELETE | migrate | Nest under workspace; use `{uuid}` not `?dir=` |
| `/api/workspace-prompts/toggle-enabled` | PUT | `/api/workspaces/{uuid}/prompts/{name}` | PATCH | migrate | Eliminate verb path; use PATCH with `{ "enabled": bool }` |
| `/api/workspace-processors` | GET | `/api/workspaces/{uuid}/processors` | GET | **done** | Migrated; nested under workspace |
| `/api/workspace-processors/toggle-enabled` | PUT | `/api/workspaces/{uuid}/processors/{name}` | PATCH | **done** | Migrated; PATCH {uuid}/processors/{name} with {enabled} |
| `/api/workspace-mcp-tools` | GET | `/api/workspaces/{uuid}/mcp-tools` | GET | **done** | Migrated; nested under workspace; acp_server kept as explicit override |
| `/api/workspace-mcp-install` | POST | `/api/workspaces/{uuid}/mcp-tools/install` | POST | **done** | Migrated; nested under workspace; acp_server kept as explicit override |
| `/api/workspace-mcp-remove` | POST | `/api/workspaces/{uuid}/mcp-tools/remove` | POST | **done** | Migrated; nested under workspace; acp_server kept as explicit override |
| `/api/workspace-metadata` | GET, PUT | `/api/workspaces/{uuid}/metadata` | GET, PUT | **done** | Migrated; flat path removed |
| `/api/workspace/user-data-schema` | GET, PUT | `/api/workspaces/{uuid}/user-data-schema` | GET, PUT | **done** | Migrated; flat path removed |
| `/api/folder-group` | PUT | `/api/workspaces/{uuid}/folder-group` | PUT | **done** | Migrated; nested under workspace; PUT (mutation) — corrected from GET; resolves working_dir from uuid, applies group folder-wide |

### 7.3 Agents & Runners

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/agent-types` | GET | `/api/agents/types` | GET | migrate | Nest under `/api/agents` for coherence |
| `/api/agents/scan` | POST | `/api/agents/scan` | POST | keep | Already nested; non-CRUD action acceptable |
| `/api/agents/confirm` | POST | `/api/agents/confirm` | POST | keep | Already nested; two-phase confirm action |
| `/api/supported-runners` | GET | `/api/runners` | GET | migrate | Drop `supported-` prefix (redundant) |
| `/api/runner-defaults` | GET | `/api/runners/defaults` | GET | migrate | Nest under `/api/runners` |

### 7.4 Configuration & Flags

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/config` | GET | `/api/config` | GET | keep | Global config; correct |
| `/api/advanced-flags` | GET | `/api/config/flags` | GET | migrate | Nest under `/api/config` |
| `/api/external-status` | GET | `/api/config/external-status` | GET | migrate | Nest under `/api/config` |
| `/api/ui-preferences` | GET, PUT | `/api/config/ui-preferences` | GET, PUT | migrate | Nest under `/api/config`; replace PUT with PATCH |

### 7.5 Issues (Beads)

All `/api/beads/*` endpoints expose a verb-based RPC style. The target maps them to a RESTful `/api/workspaces/{uuid}/issues` resource. Because `bd` (beads) is a local CLI tool whose operations map awkwardly to pure REST (e.g. `sync`, `upstream`, `cleanup`) the paths below keep action sub-paths where needed.

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/beads/list` | GET | `/api/workspaces/{uuid}/issues` | GET | migrate | Rename to issues resource |
| `/api/beads/show` | GET | `/api/workspaces/{uuid}/issues/{id}` | GET | migrate | Path param instead of query param |
| `/api/beads/stats` | GET | `/api/workspaces/{uuid}/issues/stats` | GET | migrate | Collection sub-resource |
| `/api/beads/create` | POST | `/api/workspaces/{uuid}/issues` | POST | migrate | Create on collection |
| `/api/beads/update` | POST | `/api/workspaces/{uuid}/issues/{id}` | PATCH | migrate | Use PATCH for partial update |
| `/api/beads/delete` | POST | `/api/workspaces/{uuid}/issues/{id}` | DELETE | migrate | Use DELETE |
| `/api/beads/status` | GET | `/api/workspaces/{uuid}/issues/status` | GET | migrate | Collection-level status |
| `/api/beads/comment` | POST | `/api/workspaces/{uuid}/issues/{id}/comments` | POST | migrate | Sub-resource on issue |
| `/api/beads/dep` | POST | `/api/workspaces/{uuid}/issues/{id}/dependencies` | POST | migrate | Sub-resource on issue |
| `/api/beads/config` | GET, PUT | `/api/workspaces/{uuid}/issues/config` | GET, PUT | migrate | Issues config sub-resource |
| `/api/beads/upstream` | GET | `/api/workspaces/{uuid}/issues/upstream` | GET | migrate | Read-only sync info |
| `/api/beads/sync` | POST | `/api/workspaces/{uuid}/issues/sync` | POST | migrate | Non-CRUD action; acceptable |
| `/api/beads/cleanup` | POST | `/api/workspaces/{uuid}/issues/cleanup` | POST | migrate | Non-CRUD bulk action; acceptable |

### 7.6 Auxiliary

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/aux/improve-prompt` | GET, POST | `/api/aux/improve-prompt` | POST | keep (clean up method) | Auxiliary hidden session; GET is deprecated, use POST |

### 7.7 Events & WebSocket

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/events` | WS | `/api/events` | WS | keep | Global events WebSocket; correct |

### 7.8 External / Public (external-stable)

| Current path | Method(s) | Target path | Method(s) | Classification | Reason / notes |
|---|---|---|---|---|---|
| `/api/callback/{token}` | POST | `/api/callback/{token}` | POST | external-stable | Public webhook; external callers |
| `/api/health` | GET | `/api/health` | GET | external-stable | Load balancer health probe |
| `/api/login` | POST | `/api/login` | POST | external-stable | Auth form; redirect-based |
| `/api/logout` | POST | `/api/logout` | POST | external-stable | Auth; session cookie destroy |
| `/api/auth-info` | GET | `/api/auth-info` | GET | external-stable | Login page UI bootstrap |
| `/api/csrf-token` | GET | `/api/csrf-token` | GET | external-stable | CSRF token bootstrap |
| `/api/files` | GET | `/api/files` | GET | external-stable | File server; viewer pages embed URLs |
| `/api/save-file-to-path` | POST | `/api/save-file-to-path` | POST | external-stable | Native macOS app; localhost-only |
| `/api/check-file-exists` | GET | `/api/check-file-exists` | GET | external-stable | Native macOS app; localhost-only |
| `/api/badge-click` | POST | `/api/badge-click` | POST | external-stable | Native macOS Dock badge; localhost-only |

---

## 8. Summary of Decisions

| # | Decision | Chosen value | Rationale |
|---|---|---|---|
| 1 | Workspace identifier in path | `{uuid}` | Already used in `GET /api/workspaces`; unambiguous, no dir-escaping |
| 2 | Query param for workspace dir | `working_dir` | Majority in beads/metadata handlers; aligns with session field name |
| 3 | Enable/disable mechanism | `PATCH` resource with `{ "enabled": bool }` | RESTful; eliminates /toggle-enabled verb paths |
| 4 | Error envelope | `{ "error": { "code", "message", "details?" } }` | Single shape; `code` is machine-readable string |
| 5 | 405 handling | Router-level (Go 1.22 method+pattern ServeMux) | Uniform; no per-handler boilerplate |
| 6 | Beads naming in API | `/issues` (not `/beads`) | Neutral; `beads` is tool name not domain concept |
| 7 | Agent types path | `/api/agents/types` (migrate from `/api/agent-types`) | Nested under `/api/agents` for coherence |
| 8 | Runners path | `/api/runners` (migrate from `/api/supported-runners`) | Drop redundant adjective; nest defaults under it |
| 9 | UI preferences | `/api/config/ui-preferences` (migrate) | Config family; avoids top-level proliferation |
| 10 | Advanced flags | `/api/config/flags` (migrate) | Config family |
