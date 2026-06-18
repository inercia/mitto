/**
 * CodeMirror 6 Lazy Loader
 *
 * The core editor, One Dark theme, and Markdown language are loaded from a
 * locally bundled file (web/static/vendor/codemirror/codemirror.js, produced by
 * `npm run vendor:codemirror`). This works offline and avoids the CDN entirely.
 *
 * Other languages are still loaded from the esm.sh CDN on demand. NOTE: those
 * esm.sh language packages pull their own copies of @codemirror/state and
 * @codemirror/language, which are different instances than the local bundle's.
 * Non-legacy languages built that way (e.g. lang-javascript) may not apply
 * correctly because CodeMirror requires a single shared instance. Legacy modes
 * are wrapped with the local bundle's StreamLanguage below to stay consistent.
 * Bundle the remaining languages locally (the full plan) if other file types
 * ever need editing. All modules are cached after first load.
 */

const ESM_BASE = "https://esm.sh";

// Local CodeMirror bundle (resolved relative to this module so it works under
// API-prefix deployments). Memoized so it imports at most once.
const LOCAL_BUNDLE = new URL("../vendor/codemirror/codemirror.js", import.meta.url).href;
let _bundlePromise = null;
function loadBundle() {
  if (!_bundlePromise) _bundlePromise = import(LOCAL_BUNDLE);
  return _bundlePromise;
}

// Module cache (esm.sh CDN imports for non-bundled languages)
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
  const b = await loadBundle();
  return {
    view: b.view,
    state: b.state,
    commands: b.commands,
    language: b.language,
    search: b.search,
    lint: b.lint,
  };
}

/**
 * Load the dark theme (One Dark).
 * @returns {Promise<any>}
 */
export async function loadDarkTheme() {
  const b = await loadBundle();
  return b.themeOneDark;
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

  // Markup (markdown is bundled locally — handled in loadLanguage, not here)
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
  const key = ext?.toLowerCase();

  // Markdown is bundled locally — never touch the CDN for it.
  if (key === "md" || key === "markdown") {
    const b = await loadBundle();
    return b.langMarkdown.markdown();
  }

  const entry = LANG_MAP[key];
  if (!entry) return null;

  try {
    if (entry.legacy) {
      // Legacy modes are plain stream-parser specs (instance-agnostic), so we
      // load the mode object from esm.sh but wrap it with the LOCAL bundle's
      // StreamLanguage to keep it consistent with the local core instance.
      const [langMod, b] = await Promise.all([
        importCached(entry.pkg),
        loadBundle(),
      ]);
      // Legacy mode modules export the mode directly by modKey or first object export
      const mode = entry.modKey ? langMod[entry.modKey] : Object.values(langMod).find(
        (v) => typeof v === "object" && v !== null && typeof v.token === "function"
      );
      if (mode) {
        return b.language.StreamLanguage.define(mode);
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
