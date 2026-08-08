/**
 * `noneAuth` — the no-op auth adapter, for unauthenticated deployments (or
 * hosts that handle authentication entirely outside the SDK, e.g. via a
 * reverse proxy). Environment-agnostic per docs/devel/js-client-library.md
 * §4: never touches `window`/`document`/`localStorage`/bare `console.*`.
 *
 * This is `core/config.js`'s default `auth` adapter when the caller does
 * not supply one.
 */

/**
 * @returns {{authorize: function(object): Promise<object>}}
 */
export function noneAuth() {
  return {
    /** Adds nothing to the request. */
    async authorize(_request) {
      return {};
    },
  };
}
