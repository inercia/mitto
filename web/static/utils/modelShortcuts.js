// Pure helpers for the Cmd/Ctrl+Shift+<digit> composer model-switch shortcut
// (mitto-qmqq.2). No DOM/window access — testable in plain Node/bun.

import { resolveProfileModel } from "./prompts.js";

/**
 * Resolve what should happen when the user presses Cmd/Ctrl+Shift+<digit>
 * in the composer.
 *
 * @param {number} digit - The pressed digit, 1-5.
 * @param {Array<object>|null|undefined} modelProfiles - Global model profiles
 *   (each may carry `shortcut_number`, 0 or absent = unassigned).
 * @param {object|null|undefined} modelOption - The "model" config option
 *   (`configOptions.find(o => o.category === "model")`), whose `.options`
 *   array holds the catalog `{value, name}` entries in server-reported order
 *   and whose `.current_value` is the active model's value.
 * @returns {{
 *   status: "apply"|"unassigned"|"no_match"|"already_active"|"no_model_option",
 *   profile?: object,
 *   value?: string,
 *   name?: string,
 * }}
 */
export function resolveModelShortcut(digit, modelProfiles, modelOption) {
  if (!modelOption || !Array.isArray(modelOption.options)) {
    return { status: "no_model_option" };
  }

  const profile = Array.isArray(modelProfiles)
    ? modelProfiles.find((p) => (p?.shortcut_number || 0) === digit)
    : null;

  if (!profile) {
    return { status: "unassigned" };
  }

  const resolved = resolveProfileModel(profile, modelOption);
  if (!resolved) {
    return { status: "no_match", profile };
  }

  if (resolved.value === modelOption.current_value) {
    return { status: "already_active", profile, value: resolved.value, name: resolved.name };
  }

  return { status: "apply", profile, value: resolved.value, name: resolved.name };
}
