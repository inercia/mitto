/**
 * Files REST resource module (mitto-7gta.12).
 *
 * `createFilesResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7/.10 precedent: raw relative paths mirroring
 * `internal/web/routes.go`, never built through `core/endpoints.js` (which
 * would double-apply `apiPrefix`).
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.files`.
 *
 * Covers three distinct route families:
 *  - Session-scoped file attachments: /api/sessions/{id}/files[/{fileId},
 *    /from-path] (handlers/file.go, file_frompath.go) — mirrors the
 *    `sessions.images` surface 1:1.
 *  - The workspace file-server: GET /api/files?ws=&path=&render=&diff=
 *    (internal/web/file_server.go) — read-only helpers for <a href>/<img
 *    src> URLs and raw content fetches; genuinely "files" and otherwise has
 *    no home in this SDK.
 *  - The workspace file/dir pickers that feed the "filename"/"dirname"
 *    prompt parameter types (mitto-7gta.17 slice S5 gap: `/api/workspace-files`
 *    and `/api/workspace-dirs` had URL builders in `core/endpoints.js` but no
 *    resource-module method — added here rather than as a new top-level
 *    domain since they are read-only workspace file/dir listings, the same
 *    theme as the two families above).
 */
import { buildUrl, request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the files resource
 */
export function createFilesResource(config) {
  const call = (method, path, opts = {}) =>
    request(config, { method, path, ...opts });

  return {
    list: (id, opts) => call("GET", `/api/sessions/${enc(id)}/files`, opts),
    /** @param {FormData} form - must contain a "file" field; the runtime
     *  sets the multipart Content-Type/boundary (see transport.js
     *  `isPassthroughBody`). Server caps uploads at 50 MB. */
    upload: (id, form, opts) =>
      call("POST", `/api/sessions/${enc(id)}/files`, { body: form, ...opts }),
    /** @param {string[]} paths - absolute file paths (native app only; the
     *  server restricts this endpoint to localhost connections). */
    uploadFromPath: (id, paths, opts) =>
      call("POST", `/api/sessions/${enc(id)}/files/from-path`, {
        body: { paths },
        ...opts,
      }),
    /** Returns the browser-usable URL for a file (e.g. <a href>); does not
     *  fetch bytes. Use `fetchFile()` to retrieve the raw Response. Unlike
     *  the other methods it never reaches `request()`, so it applies
     *  `buildUrl()` itself to pick up `baseUrl`/`apiPrefix`. */
    url: (id, fileId) =>
      buildUrl(config, `/api/sessions/${enc(id)}/files/${enc(fileId)}`),
    /** @returns {Promise<Response>} the raw, undecoded file response. */
    fetchFile: (id, fileId, opts) =>
      call("GET", `/api/sessions/${enc(id)}/files/${enc(fileId)}`, {
        raw: true,
        ...opts,
      }),
    remove: (id, fileId, opts) =>
      call("DELETE", `/api/sessions/${enc(id)}/files/${enc(fileId)}`, opts),

    /** Returns the browser-usable URL for a workspace file (e.g. <a
     *  href>/<img src>); does not fetch bytes.
     *  @param {object} params - {ws, path, render?, diff?} */
    contentUrl: (params) => buildUrl(config, "/api/files", params),
    /** @param {object} params - {ws, path, render?, diff?}
     *  @returns {Promise<Response>} the raw, undecoded file response. */
    fetchContent: (params, opts) =>
      call("GET", "/api/files", { query: params, raw: true, ...opts }),

    /** GET /api/workspace-files?working_dir=&dir=&glob= — candidate files
     *  for a "filename" prompt parameter's dropdown.
     *  @param {object} params - {working_dir, dir?, glob?: string|string[]}
     *  @returns {Promise<{files: object[]}>} */
    workspaceFiles: {
      list: (params, opts) =>
        call("GET", "/api/workspace-files", { query: params, ...opts }),
    },

    /** GET /api/workspace-dirs?working_dir=&dir=&glob= — candidate
     *  directories for a "dirname" prompt parameter's dropdown.
     *  @param {object} params - {working_dir, dir?, glob?: string|string[]}
     *  @returns {Promise<{dirs: object[]}>} */
    workspaceDirs: {
      list: (params, opts) =>
        call("GET", "/api/workspace-dirs", { query: params, ...opts }),
    },
  };
}
