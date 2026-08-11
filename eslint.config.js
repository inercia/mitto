import globals from "globals";

// mitto-7gta.19: SDK boundary enforcement allowlist.
//
// Files listed here are still allowed to touch fetch/WebSocket/XHR/EventSource
// directly and/or deep-import sdk/ internals. All transitional shim entries
// were retired in mitto-7gta.19.1 — the three remaining entries are
// permanent. Do not add a new entry without a one-line justification, and
// prefer migrating an existing entry onto the SDK client
// (web/static/utils/sdkClient.js's getSdkClient()) over adding a new one.
const SDK_BOUNDARY_ALLOWLIST = [
  "web/static/sdk/**", // the SDK implementation itself (permanent)
  "web/static/sw.js", // service worker: fetch() IS the API being implemented (permanent)
  "web/static/utils/sdkClient.js", // deliberate late-bound fetch injection seam into the SDK client (permanent)
];

export default [
  {
    ignores: [
      "coverage/**",
      "frontend/**",
      "node_modules/**",
      "build/**",
      "tests/**",
      "web/static/vendor/**",
      "web/static/**/*.test.js",
    ],
  },
  {
    files: ["web/static/**/*.js"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        ...globals.browser,
        // Mitto-specific globals injected by the server
        mittoApiPrefix: "readonly",
        mittoIsExternal: "readonly",
      },
    },
    rules: {
      // Catch real errors
      "no-dupe-keys": "error",
      "no-dupe-args": "error",
      "no-duplicate-case": "error",
      "no-unreachable": "error",
      "valid-typeof": "error",
      "no-constant-condition": "error",
      "no-self-assign": "error",
      "no-self-compare": "error",
      "use-isnan": "error",
      "no-sparse-arrays": "error",
      "no-template-curly-in-string": "warn",
      "no-loss-of-precision": "error",

      // Code quality
      "eqeqeq": ["error", "always", { null: "ignore" }],
      "no-unused-vars": [
        "warn",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
        },
      ],
    },
  },
  {
    // mitto-7gta.22: the JS-client examples are plain Bun/Node CLI programs,
    // not part of web/static/, so the block above never covers them and
    // they'd otherwise be silently unlinted (unlike the Go examples, which
    // `make test-go` compiles as part of ./examples/...). SDK-boundary
    // rules below are deliberately NOT extended here: an example
    // constructing its own WebSocket implementation (see prompt-stream's
    // BunWebSocket) is exactly the point being demonstrated, not a
    // violation of it.
    files: ["examples/js-client/**/*.js"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        ...globals.node,
      },
    },
    rules: {
      "no-dupe-keys": "error",
      "no-dupe-args": "error",
      "no-duplicate-case": "error",
      "no-unreachable": "error",
      "valid-typeof": "error",
      "no-constant-condition": "error",
      "no-self-assign": "error",
      "no-self-compare": "error",
      "use-isnan": "error",
      "no-sparse-arrays": "error",
      "no-loss-of-precision": "error",
      "eqeqeq": ["error", "always", { null: "ignore" }],
      "no-unused-vars": [
        "warn",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
        },
      ],
    },
  },
  {
    // mitto-7gta.19: ban direct backend access outside the SDK
    // (web/static/sdk/). All fetch/WebSocket/XHR/EventSource traffic and
    // deep sdk/ imports must go through web/static/sdk/index.js (see
    // docs/devel/js-client-library.md §5). SDK_BOUNDARY_ALLOWLIST above is
    // the only exemption mechanism — do not add inline eslint-disable
    // comments for these rules instead.
    files: ["web/static/**/*.js"],
    ignores: SDK_BOUNDARY_ALLOWLIST,
    rules: {
      "no-restricted-globals": [
        "error",
        {
          name: "fetch",
          message: "Use the SDK client (getSdkClient() in web/static/utils/sdkClient.js) instead of raw fetch().",
        },
        {
          name: "WebSocket",
          message: "Use the SDK's realtime streams (web/static/sdk/realtime/) instead of raw WebSocket.",
        },
        {
          name: "XMLHttpRequest",
          message: "Use the SDK client (getSdkClient() in web/static/utils/sdkClient.js) instead of raw XMLHttpRequest.",
        },
        {
          name: "EventSource",
          message: "Use the SDK's realtime streams (web/static/sdk/realtime/) instead of raw EventSource.",
        },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "MemberExpression[object.name=/^(window|globalThis|self)$/][property.name=/^(fetch|WebSocket|XMLHttpRequest|EventSource)$/]",
          message: "Use the SDK client instead of accessing fetch/WebSocket/XMLHttpRequest/EventSource via window/globalThis/self.",
        },
        {
          selector: "NewExpression[callee.name='WebSocket']",
          message: "Use the SDK's realtime streams (web/static/sdk/realtime/) instead of raw WebSocket.",
        },
        // no-restricted-imports only sees static import/export declarations, so
        // the dynamic `import()` form needs its own selectors to close the
        // same two holes.
        {
          selector: "ImportExpression[source.value=/sdk\\/[^/]+\\//]",
          message: "Deep sdk/ imports are internal/unsupported (docs/devel/js-client-library.md §5). Import from sdk/index.js instead.",
        },
        {
          selector: "ImportExpression[source.value=/(^|\\/)csrf\\.js$/]",
          message: "authFetch/secureFetch were removed (mitto-7gta.17 slice S8). Use the SDK client instead.",
        },
      ],
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["**/sdk/*/*"],
              message: "Deep sdk/ imports are internal/unsupported (docs/devel/js-client-library.md §5). Import from sdk/index.js instead.",
            },
            // A glob (not a `paths` entry) so the ban holds at any nesting
            // depth: `paths` matches the specifier string literally, so
            // "../utils/csrf.js" would not cover "../../utils/csrf.js".
            {
              group: ["**/utils/csrf.js"],
              importNames: ["authFetch", "secureFetch"],
              message: "authFetch/secureFetch were removed (mitto-7gta.17 slice S8). Use the SDK client instead.",
            },
          ],
          paths: [
            // Sibling form, only reachable from within web/static/utils/.
            {
              name: "./csrf.js",
              importNames: ["authFetch", "secureFetch"],
              message: "authFetch/secureFetch were removed (mitto-7gta.17 slice S8). Use the SDK client instead.",
            },
          ],
        },
      ],
    },
  },
];
