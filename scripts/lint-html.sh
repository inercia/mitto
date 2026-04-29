#!/bin/bash
# lint-html.sh — Lint HTML files after stripping Go template placeholders.
#
# Go template tags like {{CSP_NONCE}} cause false positives in HTML linters.
# This script replaces them with safe placeholder values before linting.

set -euo pipefail

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

HTML_FILES=(web/static/index.html web/static/auth.html web/static/viewer.html)
EXIT_CODE=0

for file in "${HTML_FILES[@]}"; do
  if [ ! -f "$file" ]; then
    echo "Warning: $file not found, skipping"
    continue
  fi

  tmpfile="$TMPDIR/$(basename "$file")"

  # Replace Go template placeholders with safe values
  sed \
    -e 's/{{CSP_NONCE}}/placeholder-nonce/g' \
    -e 's/{{API_PREFIX}}/\/mitto/g' \
    -e 's/{{IS_EXTERNAL}}/false/g' \
    -e 's/{{CSRF_TOKEN}}/placeholder-csrf/g' \
    -e 's/{{AUTH_PROVIDER}}/local/g' \
    -e 's/{{[A-Z_]*}}/placeholder/g' \
    "$file" > "$tmpfile"

  echo "Linting $file..."
  if ! npx htmlhint "$tmpfile"; then
    EXIT_CODE=1
  fi
done

exit $EXIT_CODE
