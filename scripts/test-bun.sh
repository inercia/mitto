#!/usr/bin/env bash
# Run the subset of web/static tests that are known-clean under `bun test`.
#
# Background: the project's default test runner is Jest with jsdom (see the
# `jest` block in package.json). `bun test` is a Jest-compatible-*ish* runner
# that is roughly an order of magnitude faster because it skips node's ESM VM
# loader entirely, but it does NOT wire up jsdom and does NOT reuse the Jest
# `testEnvironment` config. As a result, tests that parse HTML, touch DOM
# globals (`document`, `window`, `DOMParser`, …), or import a module that
# reaches for those globals at load time, fail under `bun test`.
#
# This list is the subset that has been verified 100%-clean under bun test.
# When adding a new *.test.js file, run it under `bun test <file>` first: if it
# passes clean, add its path to the array below to include it in this fast
# path. If it fails, keep using `npm test` (jest+jsdom) for it — do NOT try to
# "make it work" under bun by mocking DOM APIs; that will diverge from the
# authoritative Jest run.
#
# Passing extra args (e.g. `-t <name-pattern>`, `--watch`) is supported —
# they are forwarded verbatim to `bun test`.
set -euo pipefail

cd "$(dirname "$0")/.."

CLEAN=(
  ./web/static/lib.test.js
  ./web/static/components/BeadsView.test.js
  ./web/static/components/HeaderChildrenDropdown.test.js
  ./web/static/components/Message.test.js
  ./web/static/components/PromptParameterDialog.test.js
  ./web/static/components/SettingsDialog.test.js
  ./web/static/components/SlashCommandPicker.test.js
  ./web/static/utils/api.test.js
  ./web/static/utils/beadsLinkify.test.js
  ./web/static/utils/endpoints.test.js
  ./web/static/utils/native.test.js
  ./web/static/utils/phaseState.test.js
  ./web/static/utils/prompts.test.js
  ./web/static/utils/sessionGrouping.test.js
  ./web/static/utils/sessionTree.test.js
)

exec bun test "${CLEAN[@]}" "$@"
