// Pure helpers for arranging agent-advertised config options (model, mode,
// reasoning effort, ...) for display in the composer toolbar. No DOM/window
// access — safe to unit test directly with plain Bun test.

/**
 * Moves any `category: "thought_level"` option(s) so they render immediately
 * after the `category: "model"` option, while preserving the relative order
 * of every other option exactly as advertised by the agent.
 *
 * `thought_level` is only a placement hint — the option's `id` and allowed
 * `values` stay fully agent-defined and are never inspected here.
 *
 * No-ops (returns the input array unchanged, same reference) when there is
 * no model option, or no thought_level option(s) to move.
 *
 * @param {Array<{id: string, category?: string}>} options - Advertised
 *   select-type config options, in agent-declared order.
 * @returns {Array<{id: string, category?: string}>} A new array with
 *   thought_level option(s) relocated next to the model option, or the
 *   original array reference if no reordering is needed.
 */
export function placeThoughtLevelNearModel(options) {
  if (!Array.isArray(options) || options.length === 0) {
    return options;
  }

  const thoughtLevel = options.filter((opt) => opt?.category === "thought_level");
  if (thoughtLevel.length === 0) {
    return options;
  }

  const rest = options.filter((opt) => opt?.category !== "thought_level");
  const restModelIndex = rest.findIndex((opt) => opt?.category === "model");
  if (restModelIndex === -1) {
    // No model option to anchor to — leave original ordering untouched.
    return options;
  }

  const insertAt = restModelIndex + 1;
  return [...rest.slice(0, insertAt), ...thoughtLevel, ...rest.slice(insertAt)];
}
