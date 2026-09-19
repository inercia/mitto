// Mitto Web Interface - useRenderCounter hook (mitto-sus.7)
//
// Bumps the named render domain's counter once per render of the calling
// component. Call unconditionally at the top of a render-domain's component
// body (App, SessionList, MessageList, ChatInput, ToastContainer) -- see
// docs/devel/frontend-render-domains.md for the domain catalogue this
// instrumentation supports. No-op unless perf instrumentation is enabled
// (see utils/renderCounters.js / utils/perfMarks.js's isPerfEnabled).
//
// Not a Preact hook internally (no useState/useEffect) -- it is a plain
// function call that happens to run once per render, so it is safe to call
// even after an early return in the calling component.

import { bumpRender } from "../utils/renderCounters.js";

/**
 * @param {string} regionName - One of the named render domains this
 *   component belongs to.
 */
export function useRenderCounter(regionName) {
  bumpRender(regionName);
}
