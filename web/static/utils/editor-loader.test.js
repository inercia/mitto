/**
 * Reproduction test for mitto-2a3: the internal file viewer shows no syntax
 * highlighting for Go (and other CDN-loaded languages).
 *
 * Root cause (see editor-loader.js header comment + mitto-2a3 investigation):
 * loadLanguage() resolves non-legacy languages (go, python, javascript, ...)
 * by dynamically importing the matching @codemirror/lang-* package from the
 * esm.sh CDN. esm.sh bundles each package with its OWN copy of
 * @codemirror/state and @codemirror/language — a different module instance
 * than the one bundled locally into
 * web/static/vendor/codemirror/codemirror.js. CodeMirror's language system
 * relies on facet identity / instanceof checks against a single shared
 * instance, so the resulting LanguageSupport extension is silently ignored
 * and the file renders as plain text (no error, no console output).
 *
 * Exercising the *real* facet mismatch would require actually reaching the
 * esm.sh CDN, which is unavailable/unreliable in CI (see the CDN-mocking
 * rationale in tests/ui/specs/markdown.spec.ts for the mermaid CDN). Instead
 * this test pins down the precise, deterministic, offline-verifiable defect
 * at its exact code location: loadLanguage() depends on the esm.sh CDN at
 * all for these languages, unlike markdown (which is bundled locally and
 * never touches the CDN). Once fixed — bundling the common lang-* packages
 * locally, mirroring markdown — these assertions flip to green.
 */

import { test, expect, mock } from "bun:test";
import { loadLanguage } from "./editor-loader.js";

// Representative non-legacy entries from editor-loader.js's LANG_MAP — the
// bug is "NOT Go-specific" (affects every non-legacy, non-markdown language),
// so this covers a few of the affected package families.
const NON_LEGACY_CASES = [
  { ext: "go", pkg: "@codemirror/lang-go@6", fn: "go" },
  { ext: "py", pkg: "@codemirror/lang-python@6", fn: "python" },
  { ext: "js", pkg: "@codemirror/lang-javascript@6", fn: "javascript" },
];

for (const { ext, pkg, fn } of NON_LEGACY_CASES) {
  test(`loadLanguage('${ext}') must not depend on the esm.sh CDN (mitto-2a3)`, async () => {
    let esmHit = false;
    mock.module(`https://esm.sh/${pkg}`, () => {
      esmHit = true;
      return { [fn]: () => ({ __mockLanguageSupport: pkg }) };
    });

    const extension = await loadLanguage(ext);

    // Currently FAILS: loadLanguage() reaches the esm.sh CDN mock above,
    // proving the extension is sourced from a foreign module instance
    // instead of the single shared local bundle (contrast with the "md"
    // control case below, which never touches the CDN).
    expect(esmHit).toBe(false);
    expect(extension).toBeTruthy();
  });
}

test("control: loadLanguage('md') never touches the esm.sh CDN (bundled locally)", async () => {
  let esmHit = false;
  mock.module("https://esm.sh/@codemirror/lang-markdown@6", () => {
    esmHit = true;
    return { markdown: () => ({ __mockLanguageSupport: "esm-markdown" }) };
  });

  const extension = await loadLanguage("md");

  expect(esmHit).toBe(false);
  expect(extension).toBeTruthy();
});
