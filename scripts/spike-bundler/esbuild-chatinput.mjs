// POC (mitto-qcm): bundle web/static/components/ChatInput.js with esbuild in
// library-ESM mode. Externalise everything the browser can already resolve on
// its own (vendor libs, sibling components, hook modules) so only ChatInput +
// its transitive ../utils/* dependencies are rolled into the emitted bundle.
//
// NOT wired into make/npm — run ad-hoc:  node scripts/spike-bundler/esbuild-chatinput.mjs

import { build } from "esbuild";
import { statSync, mkdirSync, readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(__dirname, "..", "..");
const entry = resolve(repoRoot, "web/static/components/ChatInput.js");
const outDir = resolve(__dirname, "dist");
const outFile = resolve(outDir, "ChatInput.bundle.js");
mkdirSync(outDir, { recursive: true });

// Everything the browser can already resolve at runtime stays external. Note
// that ChatInput.js itself uses window.preact, not an import — so preact/htm
// aren't in the import graph, but we still externalise ../vendor/* for
// transitively-imported files that DO import them.
const external = [
  "../vendor/*",
  "../../vendor/*",
  "./SlashCommandPicker.js",
  "./LoopFrequencyPanel.js",
  "./SavePromptDialog.js",
  "./Icons.js",
  "./ConfigOptionSelect.js",
  "./PromptsMenu.js",
  "../hooks/*",
];

const result = await build({
  entryPoints: [entry],
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2022",
  outfile: outFile,
  external,
  logLevel: "info",
  metafile: true,
  legalComments: "none",
  sourcemap: false,
});

const raw = statSync(outFile).size;
const gz = gzipSync(readFileSync(outFile)).length;
const srcRaw = statSync(entry).size;

// Count bundled files (excluding externals) via the metafile.
const bundledInputs = Object.keys(result.metafile.inputs).filter(
  (k) => !k.startsWith("(disabled)"),
);

// Sanity check: emitted bundle must still export ChatInput and must NOT contain
// import specifiers for anything that stayed external.
const bundleText = readFileSync(outFile, "utf8");
const exportsChatInput =
  /export\s*\{[^}]*ChatInput[^}]*\}|export\s+function\s+ChatInput/.test(
    bundleText,
  );
const importsStillExternal = [
  "./SlashCommandPicker.js",
  "./Icons.js",
  "../hooks/useResizeHandle.js",
].filter(
  (s) =>
    bundleText.includes(`from "${s}"`) || bundleText.includes(`from '${s}'`),
);

const configLoc = readFileSync(fileURLToPath(import.meta.url), "utf8").split(
  "\n",
).length;

console.log("\n=== esbuild POC results ===");
console.log(`entry:            ${entry}`);
console.log(
  `entry size:       ${srcRaw} bytes (${(srcRaw / 1024).toFixed(1)} KiB)`,
);
console.log(`bundled inputs:   ${bundledInputs.length} file(s)`);
for (const k of bundledInputs) console.log(`  - ${k}`);
console.log(`output:           ${outFile}`);
console.log(`output size:      ${raw} bytes (${(raw / 1024).toFixed(1)} KiB)`);
console.log(`output gzipped:   ${gz} bytes (${(gz / 1024).toFixed(1)} KiB)`);
console.log(`config LOC:       ${configLoc}`);
console.log(`exports ChatInput: ${exportsChatInput}`);
console.log(
  `externals still external: ${importsStillExternal.length}/3 (kept as bare specifiers)`,
);
console.log(`warnings:         ${result.warnings.length}`);
console.log(`errors:           ${result.errors.length}`);

// Also report node_modules footprint of the bundler itself (already installed).
try {
  const esbuildSize = execSync(
    "du -sh node_modules/esbuild node_modules/@esbuild 2>/dev/null | tail -n +1",
    {
      cwd: repoRoot,
    },
  )
    .toString()
    .trim();
  console.log(`\nesbuild install cost:\n${esbuildSize}`);
} catch (_) {
  /* best-effort */
}
