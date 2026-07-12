// POC (mitto-qcm): Vite in library mode bundling web/static/components/ChatInput.js.
// Same externals list as the esbuild POC. Run with:
//   npx vite build --config scripts/spike-bundler/vite-chatinput/vite.config.js
//
// Vite (via Rollup) treats externals as REGEX-matchable patterns. We list the
// exact specifier strings ChatInput.js and its util deps use.
import { defineConfig } from "vite";
import { resolve } from "node:path";

const repoRoot = resolve(import.meta.dirname, "..", "..", "..");

const external = [
  // vendor libs (transitively imported by util deps in some cases)
  /^\.\.\/vendor\//,
  /^\.\.\/\.\.\/vendor\//,
  // sibling components — stay external so this bundle is a drop-in replacement
  "./SlashCommandPicker.js",
  "./LoopFrequencyPanel.js",
  "./SavePromptDialog.js",
  "./Icons.js",
  "./ConfigOptionSelect.js",
  "./PromptsMenu.js",
  // hooks — external
  /^\.\.\/hooks\//,
];

export default defineConfig({
  root: import.meta.dirname,
  build: {
    outDir: resolve(import.meta.dirname, "dist"),
    emptyOutDir: true,
    minify: false, // match esbuild POC (no minify) for apples-to-apples size
    sourcemap: false,
    reportCompressedSize: true,
    lib: {
      entry: resolve(repoRoot, "web/static/components/ChatInput.js"),
      name: "ChatInputBundle",
      formats: ["es"],
      fileName: () => "ChatInput.bundle.js",
    },
    rollupOptions: {
      external,
      output: {
        // Keep bare-specifier externals untouched so the emitted bundle can be
        // dropped in at the same path as the original ChatInput.js.
        preserveModules: false,
      },
    },
  },
});
