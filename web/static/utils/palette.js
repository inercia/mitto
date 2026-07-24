// Deterministic hash-based color palette for model names (mitto-8wj).
// Used by the dashboard "Model usage" chart and any future per-model badges
// so the same model gets the same color across sessions without a hardcoded
// mapping. Kept as a stand-alone module so tests can import it directly.

/** Fixed grey used for the synthetic "unknown" model bucket and empty names. */
export const UNKNOWN_MODEL_COLOR = "#9CA3AF";

/** Canonical name for the pre-migration / unattributed model bucket. */
export const UNKNOWN_MODEL_NAME = "unknown";

/**
 * Deterministic color for a model name.
 * - "" and any case-variant of "unknown" return the fixed UNKNOWN_MODEL_COLOR.
 * - All other names hash to a stable HSL hue with fixed saturation/lightness.
 * The hash is a classic 32-bit DJB-like mix over char codes; it is pure and
 * has no dependencies so identical names always return identical strings.
 * @param {string} name
 * @returns {string} CSS color string (either #9CA3AF or hsl(...)).
 */
export function modelColor(name) {
  if (name == null) return UNKNOWN_MODEL_COLOR;
  const s = String(name);
  if (s === "" || s.toLowerCase() === UNKNOWN_MODEL_NAME) return UNKNOWN_MODEL_COLOR;
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  }
  const hue = ((h % 360) + 360) % 360;
  return `hsl(${hue}, 65%, 55%)`;
}
