# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd prime` for full workflow context.

## Quick Reference

```bash
bd ready --exclude-label in-flight   # Find available work (skip beads a driver is holding)
bd show <id>                         # View issue details
bd update <id> --claim               # Claim work atomically
bd close <id>                        # Complete work
bd dolt push                         # Push beads data to remote
```

### Automated-driver `in-flight` label

Automated drivers (the L1 orchestrator and its feature/fix phase drivers — see
`mitto-ial`) tag every bead they are actively working on with the `in-flight`
label plus `claimed_by` / `claim_heartbeat_at` metadata, and clear both when
they close the bead. The label is **automated-driver-only** and is meant to be
**excluded from human queries** so a person running `bd ready` does not race a
driver on the same bead. Always pass `--exclude-label in-flight` when scanning
for work to pick up:

```bash
bd ready --exclude-label in-flight
```

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**
```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

**Other commands that may prompt:**
- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->

<!-- BEGIN USER PREFERENCES (auto-managed by memorize-preferences processor) -->
## User Preferences

- **Autonomous action in loop mode**: In scheduled loop runs, act autonomously on safe, routine actions (spawning fix conversations, clean rebases) without asking. Use `mitto_ui_notify` for communication only. Never use interactive/blocking UI tools.
- **Deduplication before spawning**: Always check existing child conversations and reuse idle ones rather than spawning duplicates. This avoids unnecessary parallel work and keeps PR monitoring focused.
- **Full gate execution before push**: When fixing CI failures, run the complete local gate (format check → lint → unit → integration) before pushing. Never push partial fixes.
- **Root cause investigation**: When issues recur across multiple runs, investigate the root cause (e.g., external commits landing without proper formatting) and communicate findings.
- **Targeted problem analysis**: When only a single file or small set of issues is found, provide precise, targeted instructions rather than attempting broad fixes.
- **Pre-commit discipline enforcement**: Communicate that developers should always run `make fmt` before committing to avoid reintroducing CI failures from formatting issues.
<!-- END USER PREFERENCES -->
