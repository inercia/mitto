# Vendor Libraries

This directory contains locally bundled JavaScript libraries used by Mitto.

## Why Local + CDN?

Mitto uses a dual-loading strategy based on connection type:

| Connection Type | Source | Rationale |
|-----------------|--------|-----------|
| **Local/Native** (127.0.0.1) | Local files | Zero latency, works offline |
| **External** (Tailscale, etc.) | jsdelivr CDN | Edge caching, browser caching, pre-compressed |

## Current Versions

See `config.js` for the authoritative version configuration:

| Library | Version | Local File | CDN |
|---------|---------|------------|-----|
| Preact | 10.19.3 | `preact.js` | jsdelivr |
| Preact Hooks | 10.19.3 | `preact-hooks.js` | jsdelivr |
| HTM | 3.1.1 | `htm.js` | jsdelivr |
| Marked | 11.1.1 | `marked.js` | jsdelivr |
| DOMPurify | 3.0.8 | `dompurify.js` | jsdelivr |
| Mermaid | 11.x | *(not bundled)* | jsdelivr (on-demand) |
| CodeMirror (core + One Dark + Markdown) | 6.x | `codemirror/codemirror.js` | *(local only)* |

> **CodeMirror is different.** Unlike the single-file ESM libraries above (which
> are downloaded with `curl`), CodeMirror is a set of interdependent npm packages
> that require a single shared instance of `@codemirror/state`/`@codemirror/language`.
> It is therefore **bundled with esbuild**, not curled. The build packages are
> regular `devDependencies` in `package.json`; only the bundled output is committed.
> Scope is markdown-only: other languages still load from esm.sh at runtime (see
> `web/static/utils/editor-loader.js`).

## Updating a Library

### 1. Update Version in config.js

Edit `config.js` and update the version in the `VERSIONS` object:

```javascript
export const VERSIONS = {
  preact: "10.20.0",  // Updated version
  // ...
};
```

### 2. Download New Version

Use the CDN URLs to download the ES module version:

```bash
cd web/static/vendor

# Preact
curl -o preact.js "https://cdn.jsdelivr.net/npm/preact@10.20.0/dist/preact.mjs"
curl -o preact-hooks.js "https://cdn.jsdelivr.net/npm/preact@10.20.0/hooks/dist/hooks.mjs"

# HTM
curl -o htm.js "https://cdn.jsdelivr.net/npm/htm@3.1.1/dist/htm.mjs"

# Marked
curl -o marked.js "https://cdn.jsdelivr.net/npm/marked@11.1.1/lib/marked.esm.js"

# DOMPurify
curl -o dompurify.js "https://cdn.jsdelivr.net/npm/dompurify@3.0.8/dist/purify.es.mjs"
```

### CodeMirror (bundled, not curled)

CodeMirror is bundled from its npm packages with esbuild. To update it:

```bash
# Update the @codemirror/* versions in devDependencies
npm install --save-dev --save-exact @codemirror/view@^6 @codemirror/state@^6 \
  @codemirror/commands@^6 @codemirror/language@^6 @codemirror/search@^6 \
  @codemirror/lint@^6 @codemirror/theme-one-dark@^6 @codemirror/lang-markdown@^6

# Regenerate web/static/vendor/codemirror/codemirror.js
npm run vendor:codemirror   # or: make vendor-codemirror
```

The entry point is `scripts/codemirror/entry.js`. To bundle additional languages
locally, add their `export * as ...` lines there, install the package, and wire
them up in `web/static/utils/editor-loader.js`.

### 3. Test Both Loading Modes

1. **Local mode**: Access Mitto via `http://127.0.0.1:8080`
   - Check browser console for: `"Loading vendor libraries from local files"`

2. **External mode**: Access via Tailscale or external port
   - Check browser console for: `"Loading vendor libraries from CDN"`

### 4. Verify Version Consistency

Each local file should have a version comment matching `config.js`:

```bash
head -5 preact.js | grep "@"
# Should show: /npm/preact@10.20.0/dist/preact.mjs
```

## File Sources

All files are downloaded from [jsdelivr](https://www.jsdelivr.com/), which provides:

- ES module versions (`.mjs` or `.esm.js`)
- Minified builds for production
- Consistent URL patterns
- Global CDN distribution

## Security Notes

- Local files are embedded in the Mitto binary (tamper-proof)
- CDN files are served over HTTPS
- CSP headers restrict script sources to `'self'` and `cdn.jsdelivr.net`
- Consider adding Subresource Integrity (SRI) hashes for additional security

