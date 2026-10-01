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

  const thoughtLevel = options.filter(
    (opt) => opt?.category === "thought_level",
  );
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

// Reasons a dependent config option (e.g. reasoning effort) may have been
// adjusted or removed when the active model changed. Mirrors the backend's
// `ConfigAdjustment.Reason` constants in
// `internal/conversation/bgsession_callbacks.go` — keep in sync.
export const CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED = "option_removed";
export const CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED =
  "value_not_advertised";
export const CONFIG_ADJUSTMENT_REASON_SNAPSHOT_UNCONFIRMED =
  "snapshot_unconfirmed";

/**
 * Looks up the agent-advertised display name for `value` within a single
 * config option's `options[]` list. Falls back to the raw value (or "" when
 * absent) when there is no match or no display name advertised.
 *
 * @param {{options?: Array<{value: string, name?: string}>}} [configOption]
 * @param {string} [value]
 * @returns {string}
 */
function resolveConfigValueDisplayName(configOption, value) {
  if (!value) return "";
  const match = configOption?.options?.find(
    (candidate) => candidate?.value === value,
  );
  return match?.name || value;
}

/**
 * Builds a human-readable, non-error notice describing one `ConfigAdjustment`
 * entry delivered alongside a `config_option_changed` WS broadcast's
 * `config_options` snapshot — e.g. "Reasoning effort isn't available for
 * this model and was cleared."
 *
 * Text is always derived from the agent-advertised option/value display
 * names in `configOptions` (the just-applied snapshot) — this function never
 * hardcodes a vendor, model, or effort-level name.
 *
 * @param {{config_id: string, requested_value?: string, applied_value?: string, reason: string}} adjustment
 * @param {Array<{id: string, name?: string, options?: Array<{value: string, name?: string}>}>} [configOptions]
 *   The full snapshot array delivered in the same payload.
 * @returns {{style: "info"|"warning", title: string, message: string}}
 */
export function describeConfigAdjustment(adjustment, configOptions) {
  const configOption = Array.isArray(configOptions)
    ? configOptions.find((opt) => opt?.id === adjustment?.config_id)
    : undefined;
  const optionName = configOption?.name || adjustment?.config_id || "Setting";

  switch (adjustment?.reason) {
    case CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED:
      return {
        style: "warning",
        title: "Setting cleared",
        message: `${optionName} isn't available for this model and was cleared.`,
      };
    case CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED: {
      const appliedName = resolveConfigValueDisplayName(
        configOption,
        adjustment?.applied_value,
      );
      return {
        style: "warning",
        title: "Setting adjusted",
        message: appliedName
          ? `Requested value for ${optionName} isn't supported by this model; using ${appliedName} instead.`
          : `Requested value for ${optionName} isn't supported by this model.`,
      };
    }
    case CONFIG_ADJUSTMENT_REASON_SNAPSHOT_UNCONFIRMED:
      return {
        style: "info",
        title: "Setting unconfirmed",
        message: `Couldn't confirm whether ${optionName} is still available for this model.`,
      };
    default:
      return {
        style: "info",
        title: "Setting adjusted",
        message: `${optionName} was adjusted for this model.`,
      };
  }
}

/**
 * Builds a stable, session-scoped dedup key for one config adjustment notice
 * so the same adjustment delivered more than once (e.g. a redelivered or
 * replayed broadcast) does not surface a duplicate toast.
 *
 * @param {string} sessionId
 * @param {{config_id: string, reason: string, requested_value?: string, applied_value?: string}} adjustment
 * @returns {string}
 */
export function configAdjustmentDedupKey(sessionId, adjustment) {
  return [
    sessionId,
    adjustment?.config_id ?? "",
    adjustment?.reason ?? "",
    adjustment?.applied_value ?? "",
    adjustment?.requested_value ?? "",
  ].join("\u0000");
}
