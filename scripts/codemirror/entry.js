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
 * Scope: markdown-only. The core, One Dark theme, and Markdown language are
 * bundled locally. Other languages still load from esm.sh (see editor-loader.js).
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
