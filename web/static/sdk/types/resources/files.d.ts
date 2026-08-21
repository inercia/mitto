/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createFilesResource(config: import("../core/config.js").ResolvedConfig): {
    list: (id: any, opts: any) => Promise<any>;
    /** @param {FormData} form - must contain a "file" field; the runtime
     *  sets the multipart Content-Type/boundary (see transport.js
     *  `isPassthroughBody`). Server caps uploads at 50 MB. */
    upload: (id: any, form: FormData, opts: any) => Promise<any>;
    /** @param {string[]} paths - absolute file paths (native app only; the
     *  server restricts this endpoint to localhost connections). */
    uploadFromPath: (id: any, paths: string[], opts: any) => Promise<any>;
    /** Returns the browser-usable URL for a file (e.g. <a href>); does not
     *  fetch bytes. Use `fetchFile()` to retrieve the raw Response. Unlike
     *  the other methods it never reaches `request()`, so it applies
     *  `buildUrl()` itself to pick up `baseUrl`/`apiPrefix`. */
    url: (id: any, fileId: any) => string;
    /** @returns {Promise<Response>} the raw, undecoded file response. */
    fetchFile: (id: any, fileId: any, opts: any) => Promise<Response>;
    remove: (id: any, fileId: any, opts: any) => Promise<any>;
    /** Returns the browser-usable URL for a workspace file (e.g. <a
     *  href>/<img src>); does not fetch bytes.
     *  @param {object} params - {ws, path, render?, diff?} */
    contentUrl: (params: object) => string;
    /** @param {object} params - {ws, path, render?, diff?}
     *  @returns {Promise<Response>} the raw, undecoded file response. */
    fetchContent: (params: object, opts: any) => Promise<Response>;
    /** GET /api/workspace-files?working_dir=&dir=&glob= — candidate files
     *  for a "filename" prompt parameter's dropdown.
     *  @param {object} params - {working_dir, dir?, glob?: string|string[]}
     *  @returns {Promise<{files: object[]}>} */
    workspaceFiles: {
        list: (params: any, opts: any) => Promise<any>;
    };
    /** GET /api/workspace-dirs?working_dir=&dir=&glob= — candidate
     *  directories for a "dirname" prompt parameter's dropdown.
     *  @param {object} params - {working_dir, dir?, glob?: string|string[]}
     *  @returns {Promise<{dirs: object[]}>} */
    workspaceDirs: {
        list: (params: any, opts: any) => Promise<any>;
    };
};
