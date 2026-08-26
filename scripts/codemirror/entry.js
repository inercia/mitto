/**
 * CodeMirror bundle entry point (build-time only).
 *
 * esbuild bundles this file into web/static/vendor/codemirror/codemirror.js,
 * which is committed and embedded in the Mitto binary via go:embed.
 *
 * Bundling all packages into a single module graph guarantees a single shared
 * instance of @codemirror/state and @codemirror/language — CodeMirror relies
 * on this (facets / instanceof checks) and breaks with duplicate instances.
 *
 * The core, One Dark theme, and the common @codemirror/lang-* language
 * packages below are bundled locally so every language shares that single
 * instance (mitto-2a3). Uncommon/legacy languages still load their
 * instance-agnostic stream-parser spec from esm.sh (see editor-loader.js),
 * which is safe because those specs are wrapped with this bundle's
 * StreamLanguage before use.
 *
 * To regenerate: `npm run vendor:codemirror` (or `make vendor-codemirror`).
 */

export * as view from "@codemirror/view";
export * as state from "@codemirror/state";
export * as commands from "@codemirror/commands";
export * as language from "@codemirror/language";
export * as search from "@codemirror/search";
export * as lint from "@codemirror/lint";
export * as themeOneDark from "@codemirror/theme-one-dark";
export * as langMarkdown from "@codemirror/lang-markdown";
export * as langJavascript from "@codemirror/lang-javascript";
export * as langPython from "@codemirror/lang-python";
export * as langGo from "@codemirror/lang-go";
export * as langRust from "@codemirror/lang-rust";
export * as langHtml from "@codemirror/lang-html";
export * as langCss from "@codemirror/lang-css";
export * as langJson from "@codemirror/lang-json";
export * as langYaml from "@codemirror/lang-yaml";
export * as langXml from "@codemirror/lang-xml";
export * as langJava from "@codemirror/lang-java";
export * as langCpp from "@codemirror/lang-cpp";
export * as langPhp from "@codemirror/lang-php";
export * as langSql from "@codemirror/lang-sql";
