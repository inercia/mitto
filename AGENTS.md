# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd prime` for full workflow context.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work atomically
bd close <id>         # Complete work
bd dolt push          # Push beads data to remote
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

- **Preact event handlers**: Use `onInput` (not `onChange`) for text input event handlers to match Preact conventions
- **Frontend templating**: Use `html` tagged template literals (Preact/HTM style), not JSX, for all frontend component code
- **Go nil vs empty slices in ACP JSON**: Always initialize slices that are JSON-serialized to the ACP server as empty slices (`[]T{}`) rather than nil (`var x []T`). Go encodes nil slices as JSON `null`, which the ACP server rejects. Use the comment pattern `// Must be empty array, not nil — ACP validates this` to document these fields.
- **Prompt enabledWhen fields**: `enabledWhenACP` and `enabledWhenMCP` must be completely removed (not just deprecated) from all code, docs, and prompt files. Replace with equivalent `enabledWhen` CEL expressions everywhere.
- **Prompt design: skip `mitto_conversation_get_summary`**: In prompts and processors, agents already know the conversation context — never instruct them to call `mitto_conversation_get_summary` to recall it. Use existing knowledge directly.
- **Cross-session confirmation pattern**: Agents should propose their single best plan (based on conversation context) and confirm via `mitto_ui_options` with `allow_free_text: true`. Do NOT force a "propose 3–5 options" step — one clear proposal with a free-text override is the preferred pattern.
- **Git-gated prompts**: Add `fileExists(".git/config")` to `enabledWhen` for any prompt that is Git/GitHub-specific. This hides the prompt for non-git workspaces.
- **Prompt auto-rename**: Prompts that run in a named context (e.g., a specific repo or project) should auto-rename the conversation with `mitto_conversation_update` at the start. Use `@mitto:conversation_title` to check the current title and skip the update if it's already correct.
- **Terminology consistency**: Use "conversation" (not "session") in all user-facing UI text, labels, and headings. Keep "session" only in internal code identifiers and API paths where it's already established.
- **Dialog button conventions**: Use "Save" (not "Save changes") as the save button label. Use "Close" (not "Cancel") to dismiss dialogs. "Save" should save without closing — the user must press "Close" separately. This enables flows that require saving settings before continuing.
<!-- END USER PREFERENCES -->
