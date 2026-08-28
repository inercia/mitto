# REST API Reference

Every resource method is `call(METHOD, path, { query?, body?, ...opts })`
over `client.config`'s `baseUrl + apiPrefix + path`. Full path/method/error
conventions: [REST API Conventions](../devel/rest-api-conventions.md).

## Shared request options

Every method accepts a trailing `opts` object, forwarded to the transport:

| Option        | Purpose                                                                            |
| ------------- | ---------------------------------------------------------------------------------- |
| `signal`      | `AbortSignal` to cancel the request                                                |
| `headers`     | Extra headers, merged under the auth adapter's patch                               |
| `raw`         | Resolve with the untouched `Response` instead of a decoded body (streaming/blob)   |
| `allowStatus` | HTTP statuses excluded from the error path (e.g. `[304]` for conditional requests) |

Bodies are JSON-encoded automatically unless they are a `string`,
`FormData`, `Blob`, `ArrayBuffer`/typed array, or `URLSearchParams`
(passthrough, no `Content-Type` forced — the runtime sets multipart
boundaries, etc.). Responses: `null` for 204/205/empty, JSON when
`content-type` says so, otherwise text.

## `client.sessions`

| Method                                                         | Endpoint                                          |
| -------------------------------------------------------------- | ------------------------------------------------- |
| `list(opts)`                                                   | `GET /api/sessions`                               |
| `running(opts)`                                                | `GET /api/sessions/running`                       |
| `get(id, opts)`                                                | `GET /api/sessions/{id}`                          |
| `create(body, opts)`                                           | `POST /api/sessions`                              |
| `update(id, patch, opts)`                                      | `PATCH /api/sessions/{id}`                        |
| `remove(id, opts)`                                             | `DELETE /api/sessions/{id}`                       |
| `events(id, params, opts)`                                     | `GET /api/sessions/{id}/events`                   |
| `changes(id, opts)`                                            | `GET /api/sessions/{id}/changes`                  |
| `getSettings(id, opts)` / `updateSettings(id, settings, opts)` | `GET`/`PATCH /api/sessions/{id}/settings`         |
| `flush(id, opts)`                                              | `POST /api/sessions/{id}/flush`                   |
| `prune(id, keepLast, opts)`                                    | `POST /api/sessions/{id}/prune`                   |
| `get/create/revokeCallback(id, opts)`                          | `GET`/`POST`/`DELETE /api/sessions/{id}/callback` |
| `getUserData(id, opts)` / `setUserData(id, body, opts)`        | `GET`/`PUT /api/sessions/{id}/user-data`          |
| `promptArgCache(id, promptName, opts)`                         | `GET /api/sessions/{id}/prompt-arg-cache`         |
| `acknowledgeUIPrompt(id, requestId, opts)`                     | `POST /api/sessions/{id}/ui-prompt/acknowledge`   |

### `client.sessions.images` (also `client.images`)

`list`, `upload(id, form, opts)`, `uploadFromPath(id, paths, opts)`,
`url(id, imageId)` (no fetch — builds a browser-usable URL),
`fetchImage(id, imageId, opts)` (raw `Response`), `remove(id, imageId, opts)`
— all under `/api/sessions/{id}/images...`.

### `client.sessions.queue`

`list`, `add(id, body, opts)`, `addNamed(id, promptName, args, extra, opts)`,
`get(id, msgId, opts)`, `remove(id, msgId, opts)`, `clear(id, opts)`,
`move(id, msgId, direction, opts)`, `config(id, opts)` (reads
`conversations.queue` from `GET /api/config` — queue behavior is
global/workspace-scoped, not per-session) — all under
`/api/sessions/{id}/queue...`. See [Message Queue](../devel/message-queue.md).

### `client.sessions.loop`

`get`, `set(id, body, opts)` (PUT, full replace), `update(id, patch, opts)`
(PATCH, partial), `detach(id, opts)`, `restore(id, opts)`,
`runNow(id, resetTimer, opts)`, `suggestFromRecent(id, opts)`,
`acknowledgeStoppedReason(id, opts)`, `enable(id, opts)`/`disable(id, opts)`
(PATCH sugar) — all under `/api/sessions/{id}/loop...`. Use the `triggers`
array field, not the legacy scalar `trigger`. See
[Message Queue § Loop Prompts](../devel/message-queue.md#loop-prompts-multi-trigger-architecture).

## `client.prompts`

`list(params, opts)`, `create(body, opts)`, `remove(params, opts)`,
`setEnabled(name, workingDir, enabled, opts)`, `rememberedArgs(params, opts)`
— `/api/workspace-prompts...`.

## `client.processors`

`list(uuid, opts)`, `setEnabled(uuid, name, enabled, opts)`,
`setArguments(uuid, name, argumentsMap, opts)` —
`/api/workspaces/{uuid}/processors...`.

## `client.shortcuts`

`getGlobal(params, opts)`/`setGlobal(body, opts)` (`/api/global/shortcuts`),
`getFolder(params, opts)`/`setFolder(workingDir, body, opts)`
(`/api/folders/shortcuts?working_dir=...`).

## `client.taskLabelColors`

`getGlobal(opts)`/`setGlobal(body, opts)` — `GET`/`PUT
/api/global/task-label-colors`. The body is
`{ entries: [{ label, color }] }`; entries stay ordered and `color` is a
six-digit hexadecimal value. Saving broadcasts `task_label_colors_updated` so
open Tasks views refetch the mapping.

`getFolder(params, opts)`/`setFolder(workingDir, body, opts)` — `GET`/`PUT
/api/folders/task-label-colors?working_dir=...`. Same `{ entries: [{ label,
color }] }` body and validation as the global endpoint (trimmed non-empty
label, six-digit hex color lowercased); `working_dir` must be an absolute path
matching a known workspace. Saving broadcasts `folder_task_label_colors_updated`
(with the affected `working_dir`) so open Tasks views for that folder refetch.

## `client.serverConfig`

`get(params, opts)`/`save(body, opts)` — `GET`/`POST /api/config`. Plus
discovery: `advancedFlags`, `externalStatus`, `supportedRunners`,
`runnerDefaults` (also reachable via `client.misc`, same function objects —
not `client.config`, which is the resolved SDK config; see [Client](client.md)).

## `client.issues`

`working_dir` is a **required** query param on every method except
`migrate()` (body field). `list`, `stats`, `labelsAll`, `config`/`setConfig`/`deleteConfig`,
`upstream`/`setUpstream`, `show(id, params, opts)`, `create(params, body, opts)`,
`update(id, params, patch, opts)`, `remove(id, params, opts)`,
`status(id, params, body, opts)`, `comment`/`comments(id, params, body, opts)`,
`dependency`/`dependencies(id, params, body, opts)`, `label`/`labels(id, params, body, opts)`,
`cleanup(params, opts)`, `sync(params, body, opts)`, `migrate(body, opts)` —
`/api/issues...` (`migrate` → `POST /api/beads/migrate`).

`withIssueCaches(issues, hooks)` wraps this resource with optional,
injectable `isGone`/`markGone`/`onListed`/`shouldPreload` hooks plus a
`preload(ids, params)` method — a pure pass-through when no hooks are
given.

## `client.files`

Session attachments: `list`, `upload(id, form, opts)`,
`uploadFromPath(id, paths, opts)`, `url(id, fileId)`, `fetchFile(id, fileId, opts)`
(raw `Response`), `remove(id, fileId, opts)` — `/api/sessions/{id}/files...`.
Workspace file server (read-only): `contentUrl(params)`, `fetchContent(params, opts)`
— `GET /api/files?ws=&path=&render=&diff=`. Pickers:
`workspaceFiles.list(params, opts)` / `workspaceDirs.list(params, opts)`.

## `client.dashboard`

`summary(params, opts)` — `GET /api/dashboard`. `timeseries(params, opts)`
— `GET /api/dashboard/timeseries` (an array-valued `metrics` param is
comma-joined before sending).

## `client.workspaces`

`list(params, opts)`, `create(body, opts)`, `remove(uuid, opts)` (uuid as
query param), `getMetadata`/`setMetadata(uuid, body, opts)`,
`getUserDataSchema`/`setUserDataSchema(uuid, body, opts)`,
`getEffectiveRunnerConfig(uuid, opts)`, `getAcpStatus(uuid, opts)`,
`restartAcp(uuid, opts)`, `setFolderGroup(uuid, group, opts)`,
`listMcpTools(uuid, acpServer, opts)`, `installMcpTool`/`removeMcpTool(uuid, body, opts)`
— `/api/workspaces/{uuid}/...`.

## `client.acpServers`

`prepareDelete(name, opts)`, `reassignAndDelete(name, body, opts)` — the
guided two-step ACP server deletion flow, `/api/acp-servers/{name}/...`.

## `client.agents`

`types(opts)` — `GET /api/agents/types`. `scan(opts)` — `POST /api/agents/scan`.
`confirm(agentsList, opts)` — `POST /api/agents/confirm`.

## `client.misc`

`uiPreferences.get`/`save(prefs, opts)`, `csrfToken(opts)`, `authInfo(opts)`,
`login(credentials, opts)`, `checkFileExists(path, opts)` (localhost-only
server-side), `saveFileToPath(path, content, opts)` (localhost-only),
`improvePrompt(prompt, workspaceUUID, opts)`, `badgeClick(body, opts)`
(localhost-only), `folderPin.get`/`set(params, body, opts)`. Also
re-exposes `advancedFlags`/`externalStatus`/`supportedRunners`/`runnerDefaults`
(same objects as `client.serverConfig`'s).

## `client.endpoints`

The raw URL registry (`createEndpoints`), grouped the same way, returning
full URLs rather than performing requests — e.g. `client.endpoints.sessions.ws(id)`.
Used internally by `sessionStream()`/`eventsStream()`; most callers should
use the resource methods above instead.

## Caching: `createTtlCache` / `keyForParams`

Caching is a decorator, never baked into the transport. Wrap any resource
method:

```js
import { createTtlCache, keyForParams } from "/sdk/index.js";

const cache = createTtlCache({ ttlMs: 30_000, keyFor: keyForParams });
const cachedList = cache.wrap(client.prompts.list);
await cachedList({ working_dir }); // fetches
await cachedList({ working_dir }); // served from cache within ttlMs
await cachedList({ working_dir }, { force: true }); // bypasses cache
cache.invalidate(); // clear everything (or pass a predicate)
```

Provides TTL caching, in-flight request dedup (concurrent callers share one
request), and optional conditional revalidation (ETag/`If-None-Match` or
`Last-Modified`) via the `revalidate` option — see the module's JSDoc for
the full `revalidate` contract.
