## Frontend bundler spike (mitto-qcm)

Throwaway POC scripts evaluating esbuild vs vite in library-ESM mode against
`web/static/components/ChatInput.js`. Not wired into `make` — run ad-hoc.

- `esbuild-chatinput.mjs` — uses the already-installed esbuild 0.28.1.
- `vite-chatinput/` — self-contained Vite project (its own `package.json`,
  vite installed there via `npm install`, so the workspace root stays clean).

Both write into `scripts/spike-bundler/dist/` (gitignored).

See `docs/devel/frontend-bundler-spike.md` for the write-up.
