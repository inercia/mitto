/**
 * Mitto JavaScript client SDK — public entrypoint.
 *
 * This is the ONLY supported import surface (docs/devel/js-client-library.md
 * §5). Everything under `sdk/core/`, `sdk/env/`, etc. is a deep import and
 * may change without notice in any release.
 */
import { resolveConfig } from "./core/config.js";
import {
  MittoError,
  ConfigError,
  MittoApiError,
  MittoAuthError,
  MittoNetworkError,
} from "./core/errors.js";

/**
 * The embedded copy ships lockstep with the server (§6): its version is the
 * Mitto release tag it is served inside.
 */
export const VERSION = "0.3.0";

/**
 * Creates a Mitto API client from environment-agnostic, injectable config.
 * See docs/devel/js-client-library.md §4 for the full contract.
 */
export function createClient(options = {}) {
  const config = resolveConfig(options);
  return { config };
}

export { MittoError, ConfigError, MittoApiError, MittoAuthError, MittoNetworkError };
export { browserEnv } from "./env/browser.js";
