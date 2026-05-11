// Model context window utilities
// Shared between ChatInput and ConversationPropertiesPanel

/**
 * Known context window sizes (in tokens) for common models.
 * Keys are substrings matched case-insensitively against the model ID.
 */
const MODEL_CONTEXT_WINDOWS = {
  "gemini-2.5": 1048576,
  "gemini-2.0": 1048576,
  "gemini-1.5": 1048576,
  "gemini": 1048576,
  "o4-mini": 200000,
  "opus": 200000,
  "sonnet": 200000,
  "haiku": 200000,
  "claude": 200000,
  "o1": 200000,
  "o3": 200000,
  "gpt-4o": 128000,
  "gpt-4-turbo": 128000,
  "gpt-4": 8192,
  "gpt-3.5": 16385,
};

/**
 * Look up the context window size for a model ID.
 * Matches by checking if the model ID contains any known key (case-insensitive),
 * trying longer (more specific) keys first.
 * Returns null if no match found.
 * @param {string|null} modelId
 * @returns {number|null}
 */
export function getContextWindowSize(modelId) {
  if (!modelId) return null;
  const lower = modelId.toLowerCase();
  const sortedKeys = Object.keys(MODEL_CONTEXT_WINDOWS).sort((a, b) => b.length - a.length);
  for (const key of sortedKeys) {
    if (lower.includes(key)) return MODEL_CONTEXT_WINDOWS[key];
  }
  return null;
}
