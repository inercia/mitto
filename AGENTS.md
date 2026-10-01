# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd prime` for full workflow context.

## Quick Reference

```bash
bd ready --exclude-label in-flight   # Find available work (skip beads a driver is holding)
bd show <id>                         # View issue details
bd update <id> --claim               # Claim work atomically
bd close <id>                        # Complete work
bd dolt push                         # Shared database mode only
```

### Beads database replication policy

Mitto injects the folder's effective Beads database mode into agent guidance.
When it says **local-only**, skip every generic `bd dolt push`, `bd dolt pull`,
or bootstrap/publication instruction below, even where the auto-managed block
calls that step mandatory. Local-only Beads data must stay on this machine.
This override does not restrict normal source-code Git operations. In **shared**
mode, follow the repository's configured Beads replication workflow.

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
