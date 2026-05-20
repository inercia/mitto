/**
 * CodeMirror 6 Lazy Loader
 *
 * Loads CodeMirror packages from esm.sh CDN on demand.
 * All modules are cached after first load.
 */

const ESM_BASE = "https://esm.sh";

// Module cache
const cache = new Map();

/**
 * Import a module from esm.sh with caching.
 * @param {string} pkg - Package specifier (e.g., "@codemirror/view")
 * @returns {Promise<any>}
 */
async function importCached(pkg) {
  if (cache.has(pkg)) return cache.get(pkg);
  const mod = await import(`${ESM_BASE}/${pkg}`);
  cache.set(pkg, mod);
  return mod;
}

/**
 * Load CodeMirror core modules needed for any editor instance.
 * Returns the essential modules: view, state, commands, language, search, lint.
 * @returns {Promise<{view, state, commands, language, search, lint}>}
 */
export async function loadCore() {
  const [view, state, commands, language, search, lint] = await Promise.all([
    importCached("@codemirror/view@6"),
    importCached("@codemirror/state@6"),
    importCached("@codemirror/commands@6"),
    importCached("@codemirror/language@6"),
    importCached("@codemirror/search@6"),
    importCached("@codemirror/lint@6"),
  ]);

  return { view, state, commands, language, search, lint };
}

/**
 * Load the dark theme (One Dark).
 * @returns {Promise<any>}
 */
export async function loadDarkTheme() {
  return importCached("@codemirror/theme-one-dark@6");
}

/**
 * Extension → CodeMirror language package mapping.
 * Maps file extensions to @codemirror/lang-* packages.
 */
const LANG_MAP = {
  // JavaScript/TypeScript
  js:  { pkg: "@codemirror/lang-javascript@6", fn: "javascript" },
  mjs: { pkg: "@codemirror/lang-javascript@6", fn: "javascript" },
  cjs: { pkg: "@codemirror/lang-javascript@6", fn: "javascript" },
  ts:  { pkg: "@codemirror/lang-javascript@6", fn: "javascript", opts: { typescript: true } },
  tsx: { pkg: "@codemirror/lang-javascript@6", fn: "javascript", opts: { typescript: true, jsx: true } },
  jsx: { pkg: "@codemirror/lang-javascript@6", fn: "javascript", opts: { jsx: true } },

  // Python
  py: { pkg: "@codemirror/lang-python@6", fn: "python" },

  // Go
  go: { pkg: "@codemirror/lang-go@6", fn: "go" },

  // Rust
  rs: { pkg: "@codemirror/lang-rust@6", fn: "rust" },

  // Web
  html: { pkg: "@codemirror/lang-html@6", fn: "html" },
  htm:  { pkg: "@codemirror/lang-html@6", fn: "html" },
  css:  { pkg: "@codemirror/lang-css@6", fn: "css" },
  scss: { pkg: "@codemirror/lang-css@6", fn: "css" },
  less: { pkg: "@codemirror/lang-css@6", fn: "css" },

  // Data formats
  json: { pkg: "@codemirror/lang-json@6", fn: "json" },
  yaml: { pkg: "@codemirror/lang-yaml@6", fn: "yaml" },
  yml:  { pkg: "@codemirror/lang-yaml@6", fn: "yaml" },

  // Markup
  md:       { pkg: "@codemirror/lang-markdown@6", fn: "markdown" },
  markdown: { pkg: "@codemirror/lang-markdown@6", fn: "markdown" },
  xml:      { pkg: "@codemirror/lang-xml@6", fn: "xml" },

  // Other languages
  java: { pkg: "@codemirror/lang-java@6", fn: "java" },
  cpp:  { pkg: "@codemirror/lang-cpp@6", fn: "cpp" },
  cc:   { pkg: "@codemirror/lang-cpp@6", fn: "cpp" },
  c:    { pkg: "@codemirror/lang-cpp@6", fn: "cpp" },
  h:    { pkg: "@codemirror/lang-cpp@6", fn: "cpp" },
  hpp:  { pkg: "@codemirror/lang-cpp@6", fn: "cpp" },
  php:  { pkg: "@codemirror/lang-php@6", fn: "php" },
  sql:  { pkg: "@codemirror/lang-sql@6", fn: "sql" },

  // Shell (legacy modes)
  sh:   { pkg: "@codemirror/legacy-modes@6/mode/shell", legacy: true, modKey: "shell" },
  bash: { pkg: "@codemirror/legacy-modes@6/mode/shell", legacy: true, modKey: "shell" },
  zsh:  { pkg: "@codemirror/legacy-modes@6/mode/shell", legacy: true, modKey: "shell" },

  // Config (legacy modes)
  toml:       { pkg: "@codemirror/legacy-modes@6/mode/toml", legacy: true, modKey: "toml" },
  dockerfile: { pkg: "@codemirror/legacy-modes@6/mode/dockerfile", legacy: true, modKey: "dockerfile" },

  // Diff
  diff: { pkg: "@codemirror/legacy-modes@6/mode/diff", legacy: true, modKey: "diff" },
};

/**
 * Load the language support for a given file extension.
 * Returns a CodeMirror Extension, or null if no language support is available.
 * @param {string} ext - File extension (without dot), e.g., "js", "py", "go"
 * @returns {Promise<any|null>} Language extension or null
 */
export async function loadLanguage(ext) {
  const entry = LANG_MAP[ext?.toLowerCase()];
  if (!entry) return null;

  try {
    if (entry.legacy) {
      // Legacy modes need StreamLanguage wrapper
      const [langMod, languageMod] = await Promise.all([
        importCached(entry.pkg),
        importCached("@codemirror/language@6"),
      ]);
      // Legacy mode modules export the mode directly by modKey or first object export
      const mode = entry.modKey ? langMod[entry.modKey] : Object.values(langMod).find(
        (v) => typeof v === "object" && v !== null && typeof v.token === "function"
      );
      if (mode) {
        return languageMod.StreamLanguage.define(mode);
      }
      return null;
    }

    const langMod = await importCached(entry.pkg);
    const langFn = langMod[entry.fn];
    if (typeof langFn === "function") {
      return langFn(entry.opts || {});
    }
    return null;
  } catch (err) {
    console.warn(`Failed to load language for .${ext}:`, err);
    return null;
  }
}
