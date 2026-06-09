// Mitto Web Interface - Prompt Menu Utilities

/**
 * Returns the list of UI menus a prompt opts into. The `menus` front-matter is a
 * comma-separated list (e.g. "prompts, conversation"). A missing or empty value
 * defaults to the "prompts" dropup only, so prompts that explicitly target other
 * menus (e.g. "conversation") are excluded from the dropup unless they also list
 * "prompts".
 */
export function promptMenus(prompt) {
  const raw = typeof prompt?.menus === "string" ? prompt.menus.trim() : "";
  if (raw === "") return ["prompts"];
  return raw
    .split(",")
    .map((m) => m.trim())
    .filter(Boolean);
}

/**
 * Capabilities each menu can supply to prompts. A prompt that declares a
 * `requires` capability is only shown in a menu that provides ALL of the
 * capabilities it requires. Menus advertise what they can supply; prompts
 * declare what they need.
 */
export const MENU_CAPABILITIES = {
  prompts: [],
  conversation: [],
  beadsIssues: ["parameters"],
  beadsList: [],
};

/**
 * Parse a prompt's comma-separated `requires` list into an array of capability
 * names. Empty or absent → [].
 */
export function promptRequires(prompt) {
  const raw =
    typeof prompt?.requires === "string" ? prompt.requires.trim() : "";
  if (raw === "") return [];
  return raw
    .split(",")
    .map((r) => r.trim())
    .filter(Boolean);
}

/**
 * Returns true if `menu` provides every capability the prompt requires.
 */
export function menuSatisfiesRequires(prompt, menu) {
  const provided = MENU_CAPABILITIES[menu] || [];
  return promptRequires(prompt).every((cap) => provided.includes(cap));
}
