/**
 * Builds a partial `createClient()` options object wiring `storage` to
 * `localStorage` and `logger` to `console`, both resolved from the given
 * `globals` (defaults to `globalThis`). Spread the result into
 * `createClient()`'s options, e.g.:
 *
 *   createClient({ ...browserEnv(), baseUrl: "/api" })
 */
export function browserEnv(globals?: typeof globalThis): {
    storage: {
        getItem: (key: any) => any;
        setItem: (key: any, value: any) => any;
        removeItem: (key: any) => any;
    };
    logger: {
        debug: (...args: any[]) => any;
        info: (...args: any[]) => any;
        warn: (...args: any[]) => any;
        error: (...args: any[]) => any;
    };
};
/**
 * Returns a `getCookie(name)` reader backed by `globals.document.cookie` —
 * the injectable seam `sdk/auth/browser-cookie.js`'s `browserCookieAuth`
 * requires so it never touches `document` itself (mitto-7gta.5). This is
 * the only place under `sdk/` (besides this file's `localStorage`/`console`
 * wiring above) allowed to reference `document`.
 *
 * Not bundled into `browserEnv()`'s output: `browserCookieAuth` also needs
 * `fetch` and a `csrfTokenUrl` the preset cannot know, so callers wire it
 * explicitly, e.g.:
 *
 *   import { browserCookieAuth } from "@mitto/sdk/auth/browser-cookie.js";
 *   createClient({
 *     ...browserEnv(),
 *     baseUrl: "/api",
 *     auth: browserCookieAuth({
 *       getCookie: browserCookieReader(),
 *       fetch: window.fetch.bind(window),
 *       csrfTokenUrl: "/api/csrf-token",
 *     }),
 *   })
 */
export function browserCookieReader(globals?: typeof globalThis): (name: any) => string;
