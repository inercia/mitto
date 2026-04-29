import globals from "globals";

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
];
