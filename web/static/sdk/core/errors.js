/**
 * SDK error taxonomy — seed file.
 *
 * `.3` (typed errors) owns the full taxonomy and extends this file; the
 * `ConfigError` class name and `code` value are pinned per the stability
 * promise in docs/devel/js-client-library.md §7 and must not change.
 */

/**
 * Thrown by `resolveConfig()` / `createClient()` when the caller-supplied
 * options are invalid: an unknown option key, or a required environment
 * capability (e.g. `fetch`) that could not be resolved from injected
 * options or the injected `globals`.
 */
export class ConfigError extends Error {
  constructor(message) {
    super(message);
    this.name = "ConfigError";
    this.code = "invalid_config";
  }
}
