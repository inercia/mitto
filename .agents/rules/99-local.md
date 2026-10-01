---
description: Local development notes and GitHub CLI authentication
keywords:
  - local
  - development
  - gh auth
  - github cli
  - GOROOT
  - homebrew go
---

# Local Development Notes

This file contains local environment-specific information.

## GitHub CLI Authentication

This repository uses multiple GitHub accounts. The correct account for this repo is:

- **Account**: `inercia`
- **Repository**: `inercia/mitto`

### Switching Accounts

Before running GitHub CLI commands, ensure you're using the correct account:

```bash
# Check current auth status
gh auth status

# Switch to the correct account if needed
gh auth switch --user inercia
```

## CI/CD Workflow Files

| File                             | Purpose                               |
| -------------------------------- | ------------------------------------- |
| `.github/workflows/release.yml`  | Release workflow (triggered on tags)  |
| `.github/workflows/tests.yml`    | Test workflow (triggered on push/PR)  |
| `.github/workflows/homebrew.yml` | Homebrew tap update (manual trigger)  |
| `cliff.toml`                     | git-cliff configuration for changelog |

**See `98-release.md` for complete release workflow instructions.**

## Using `gh` CLI as Fallback

When MCP GitHub tools fail (e.g., Enterprise Managed User restrictions on public repos, `403 Unauthorized` on org repos, or rate limiting), fall back to `gh` CLI directly — the standard `gh issue|pr|run|repo …` subcommands work against `--repo inercia/mitto` once the correct account is active. `gh issue comment` returns the comment URL on success, which is a sufficient post-condition check.

## Go Toolchain — Stale Homebrew `go` Symlink

On this workstation the Homebrew `go` symlink can point at a stale/broken Go
version whose `go/bin/go` is missing while the currently-installed version
lives at a different Cellar path. Symptoms: `go build`/`go test` fails with
`fork/exec .../go/bin/go: no such file or directory` even though `which go`
resolves.

Fix: point `GOROOT` (and prepend its `bin`) at the concrete installed Cellar
path for the duration of the command. Discover the real path with:

```bash
brew --prefix go            # → /opt/homebrew/opt/go
ls -la $(brew --prefix go)/libexec  # inspect the real Cellar target
```

Then invoke Go commands with the override:

```bash
GOROOT=/opt/homebrew/opt/go/libexec PATH=/opt/homebrew/opt/go/libexec/bin:$PATH \
  go test ./internal/prompts/...
```

Prefer scoping the override to a single invocation (as above) rather than
exporting it, so unrelated tooling that expects the Homebrew symlink still
works. This is a local-machine quirk — do not commit `GOROOT` overrides to
project scripts or Makefiles.

## Beads (`bd`) Shell Invocation Gotchas

- **Backticks in body**: `bd comment <id> --body '...'` fails when the body contains
  backticks (agent shell command-substitution safety, not `bd`). Write the body to
  a file and use `--file` / `--body-file`:
  ```bash
  bd comment mitto-sys.11 --file /tmp/report.md
  ```
- **No backslash line-continuations**: `bd create/update` invocations with `\`
  continuations across lines get split by the agent shell — flags after the first
  line land as separate commands, so the issue is created/updated with only the
  earliest flags (e.g. title/priority) and silently loses `--parent`, `--set-labels`,
  `--body-file`. Always use **single-line** `bd` invocations, then repair with
  `bd update <id> --parent ... --set-labels ... --body-file ...` if needed.
- **No `@file` expansion in `-d`/`--description`**: unlike `gh`, `bd` does NOT
  interpret `@path` as "read from file". `bd create -d @/tmp/foo.txt` stores the
  literal string `@/tmp/foo.txt` as the description (silently corrupts the bead —
  seen on `on-call-7li`). For any file-backed body, use `--body-file <path>` or
  pipe via `--stdin`; for short inline text use `-d $'...'`. The builtin
  `support-watch-channel` and `support-continue-conversation` prompts now carry
  an explicit guard against this pitfall.
- **No bare `--labels` on `bd update`**: `bd update` rejects `--labels` — the
  available flags are `--add-label <label>` (repeatable), `--remove-label
<label>` (repeatable), and `--set-labels <l1,l2,...>` (repeatable, replaces
  all). `bd create` similarly uses `-l/--label` (repeatable). Passing `--labels`
  fails the whole update, leaving the bead in an intermediate state.
- **`bd label add` takes ONE label per call**: syntax is
  `bd label add [issue-id...] [label]` — the LAST positional argument is the
  single label; every earlier positional is treated as an issue ID. Running
  `bd label add mitto-abc foo bar baz` tries to apply the label `baz` to issues
  `mitto-abc`, `foo`, and `bar` (which fails or mis-labels), NOT to apply three
  labels to one issue. To apply multiple labels, either loop the command per
  label or use `bd update <id> --add-label foo --add-label bar --add-label baz`
  in a single call.

<!-- BEGIN USER PREFERENCES (auto-managed by memorize-preferences processor) -->

## User Preferences

- **Autonomous loop actions, conservative final push**: In scheduled/automated runs (loop mode, PR babysitting, etc.), act autonomously on safe, routine actions (spawning fix/child conversations, clean rebases, pushing CI fixes) without asking for confirmation — don't gate trivial or routine decisions behind a `mitto_ui_options`-style prompt that can time out and stall the work. Use `mitto_ui_notify` for communication only; never use interactive/blocking UI tools for routine decisions. Exception: the final `bd dolt push && git push` at manual session-completion still requires explicit user confirmation.
- **Deduplication before spawning, fresh child per unrelated task**: For the SAME task, check existing child conversations and reuse an idle one rather than spawning a duplicate. For an UNRELATED task, start a fresh child instead of re-prompting an existing one — keeps context clean and prevents cross-task pollution.
- **File follow-ups instead of mutating in-progress issues**: When new scope or a new finding emerges for a beads issue that is already `in_progress`, file a follow-up bead rather than editing the in-progress ticket's scope.
- **Detailed tickets with rationale**: When creating beads issues (especially child issues under an epic), include rationale, scope, risks, implementation guidance, validation approach, and measurable acceptance criteria — not just a title and one-line description. For epics, add a dependency graph that gates optimization work behind measurement work and gates speculative rewrites behind evidence from earlier children.
- **Full gate execution before push**: When fixing CI failures, run the complete local gate (format check → lint → unit → integration) before pushing. Never push partial fixes.
- **Root cause investigation**: When issues recur across multiple runs, investigate the root cause (e.g., external commits landing without proper formatting) and communicate findings.
- **Targeted problem analysis**: When only a single file or small set of issues is found, provide precise, targeted instructions rather than attempting broad fixes.
- **Pre-commit discipline enforcement**: Communicate that developers should always run `make fmt` before committing to avoid reintroducing CI failures from formatting issues.
- **PR merge bar**: When babysitting/merging PRs, merge once CI is green — don't gate on requiring a formal review approval.
- **WIP-safe commits (explicit-path staging, hunk-level when needed)**: When the working tree already has unrelated in-flight WIP, verify the exact set of dirty files with `git status --short` before starting, stage only the intended files by explicit path (`git add <path>`) — never `git add -A`, `git add .`, or `git commit -a` — and re-verify the same pre-existing dirty files remain untouched after the commit. When a **single file** mixes your intended change with unrelated dirty hunks, `git add <path>` is insufficient — build a raw unified-diff patch of only your hunks and stage it with `git apply --cached <patch>`, then `git diff --cached <path>` to confirm before committing (the file will still show `M` afterwards — that is the correct outcome). If the pre-flight status doesn't match expectations, stop and report back instead of proceeding.
- **Prefer correction over confirmation bias**: When a diagnosis or bead's evidence turns out to be an artifact (bad regex, wrong grep pattern, mis-attributed metric), retitle/correct the bead **publicly** with the error called out inline, reconsider the priority downward if warranted, and reset the analysis — do NOT keep patching around a wrong premise.
- **File follow-ups only on non-artifact findings**: When asked "file a new issue if necessary", verify the observation still holds under scrutiny before filing. A hypothesis that dissolves on second look (e.g. a false positive) should be posted as a triage comment on the parent bead, NOT elevated to a new bead. Filing every hunch pollutes the tracker.
- **Progressive re-measurement after fixes**: When a performance fix lands, take a fresh baseline before drawing conclusions — verify (a) the binary contains the fix (see `40-debugging.md` rebuild-verification protocol), (b) the target metric moved in the right direction, and (c) whether a new #1 offender emerged that was previously masked. Report as a diff table (Before → Now → Δ) rather than narrative.
- **Isolate tooling drift into its own chore commit**: When a build-tool / formatter upgrade (e.g. new Go version reformatting previously-clean files) surfaces reformats unrelated to the current feature work, land those reformats as their own explicitly-labeled `chore: gofmt <file> for Go <X.Y>` commit isolated from feature commits — never silently fold them into feature work, and never bypass the hook without recording what happened.
- **Force-push discipline**: When a force-push is unavoidable (e.g. after rebase on a PR branch), always use `git push --force-with-lease`; never plain `git push --force`. The lease guard prevents clobbering concurrent work on the remote.
- **Out-of-scope CI failures get a beads issue, not a fix commit**: When investigating a failing PR check, if the same failure also reproduces on the latest base branch and is not caused by the current PR, do NOT push a spurious "fix" as part of this PR. File a beads issue for the pre-existing failure, report its ID, and keep the PR scoped to its stated purpose.
- **Atomic per-phase commits, accumulate locally through `verified`**: For work driven by the label-encoded state machine (`planned` → `implemented` → `tested` → `verified`), land each phase as its OWN atomic commit — impl commit, tests commit, review fix-up commit(s) — never squashed into one. Accumulate the full commit chain locally through every phase transition of the epic; do NOT `git push` between phases (a mid-epic push locks in intermediate state, complicates review-phase fix-ups, and defeats the label gate). Push only once the last child bead reaches `verified` and the epic's final gate has run green locally. Orthogonal to the existing "conservative final push" preference (which governs loop-UI autonomy) — this one governs the git-history shape of label-driven epics.
<!-- END USER PREFERENCES -->
