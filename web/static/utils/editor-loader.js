/**
 * CodeMirror 6 Lazy Loader
 *
 * The core editor, One Dark theme, and the common @codemirror/lang-* language
 * packages are loaded from a locally bundled file
 * (web/static/vendor/codemirror/codemirror.js, produced by
 * `npm run vendor:codemirror`). This works offline, avoids the CDN entirely,
 * and — critically — guarantees every language extension shares the single
 * @codemirror/state / @codemirror/language instance bundled locally.
 * CodeMirror's facet/instanceof checks silently ignore extensions built
 * against a different instance (mitto-2a3), which is why non-bundled
 * languages must not be loaded as standalone modules from esm.sh.
 *
 * Legacy stream-parser modes (shell/toml/dockerfile/diff) still fetch their
 * instance-agnostic mode spec from esm.sh, but are wrapped with the local
 * bundle's StreamLanguage below, so they stay consistent with the single
 * shared instance. All modules are cached after first load.
 */

const ESM_BASE = "https://esm.sh";

// Local CodeMirror bundle (resolved relative to this module so it works under
// API-prefix deployments). Memoized so it imports at most once.
const LOCAL_BUNDLE = new URL(
  "../vendor/codemirror/codemirror.js",
  import.meta.url,
).href;
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
 *
 * `bundleKey` names the corresponding export in the local bundle
 * (scripts/codemirror/entry.js) — these languages are resolved from
 * loadBundle() and never touch the CDN. Entries without `bundleKey` (none
 * currently) would fall back to a standalone esm.sh import via `pkg`/`fn`.
 */
const LANG_MAP = {
  // JavaScript/TypeScript
  js: { bundleKey: "langJavascript", fn: "javascript" },
  mjs: { bundleKey: "langJavascript", fn: "javascript" },
  cjs: { bundleKey: "langJavascript", fn: "javascript" },
  ts: {
    bundleKey: "langJavascript",
    fn: "javascript",
    opts: { typescript: true },
  },
  tsx: {
    bundleKey: "langJavascript",
    fn: "javascript",
    opts: { typescript: true, jsx: true },
  },
  jsx: {
    bundleKey: "langJavascript",
    fn: "javascript",
    opts: { jsx: true },
  },

  // Python
  py: { bundleKey: "langPython", fn: "python" },

  // Go
  go: { bundleKey: "langGo", fn: "go" },

  // Rust
  rs: { bundleKey: "langRust", fn: "rust" },

  // Web
  html: { bundleKey: "langHtml", fn: "html" },
  htm: { bundleKey: "langHtml", fn: "html" },
  css: { bundleKey: "langCss", fn: "css" },
  scss: { bundleKey: "langCss", fn: "css" },
  less: { bundleKey: "langCss", fn: "css" },

  // Data formats
  json: { bundleKey: "langJson", fn: "json" },
  yaml: { bundleKey: "langYaml", fn: "yaml" },
  yml: { bundleKey: "langYaml", fn: "yaml" },

  // Markup (markdown is bundled locally — handled in loadLanguage, not here)
  xml: { bundleKey: "langXml", fn: "xml" },

  // Other languages
  java: { bundleKey: "langJava", fn: "java" },
  cpp: { bundleKey: "langCpp", fn: "cpp" },
  cc: { bundleKey: "langCpp", fn: "cpp" },
  c: { bundleKey: "langCpp", fn: "cpp" },
  h: { bundleKey: "langCpp", fn: "cpp" },
  hpp: { bundleKey: "langCpp", fn: "cpp" },
  php: { bundleKey: "langPhp", fn: "php" },
  sql: { bundleKey: "langSql", fn: "sql" },

  // Shell (legacy modes)
  sh: {
    pkg: "@codemirror/legacy-modes@6/mode/shell",
    legacy: true,
    modKey: "shell",
  },
  bash: {
    pkg: "@codemirror/legacy-modes@6/mode/shell",
    legacy: true,
    modKey: "shell",
  },
  zsh: {
    pkg: "@codemirror/legacy-modes@6/mode/shell",
    legacy: true,
    modKey: "shell",
  },

  // Config (legacy modes)
  toml: {
    pkg: "@codemirror/legacy-modes@6/mode/toml",
    legacy: true,
    modKey: "toml",
  },
  dockerfile: {
    pkg: "@codemirror/legacy-modes@6/mode/dockerfile",
    legacy: true,
    modKey: "dockerfile",
  },

  // Diff
  diff: {
    pkg: "@codemirror/legacy-modes@6/mode/diff",
    legacy: true,
    modKey: "diff",
  },
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
      const mode = entry.modKey
        ? langMod[entry.modKey]
        : Object.values(langMod).find(
            (v) =>
              typeof v === "object" &&
              v !== null &&
              typeof v.token === "function",
          );
      if (mode) {
        return b.language.StreamLanguage.define(mode);
      }
      return null;
    }

    // Non-legacy languages are resolved from the local bundle so they share
    // the single @codemirror/state / @codemirror/language instance (mitto-2a3).
    const langMod = entry.bundleKey
      ? (await loadBundle())[entry.bundleKey]
      : await importCached(entry.pkg);
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
