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
- **UI button consistency**: All toolbar buttons and button groups must use the same size, border style, and spacing. When multiple button groups appear in a row, maintain uniform appearance across all of them.
- **YAML enum naming**: Use camelCase (not kebab-case) for YAML enum values and field names in processor/prompt configuration (e.g., `allExceptFirst` not `all-except-first`, `afterSentMsgs` not `after-sent-msgs`).
- **No backwards compatibility shims**: When changing configuration syntax or field names, migrate all code, tests, docs, and definitions in one pass. Do not add backwards compatibility layers or fallback parsing for old formats.
- **Analysis-first workflow**: When evaluating UI components or architectural decisions, conduct thorough analysis and file issues for recommendations rather than implementing immediately. File issues as children of parent tasks to capture decomposed work. Only implement when explicitly instructed to do so.
- **daisyUI as standard UI library**: Use daisyUI components (menu, modal, theme-controller, etc.) for all UI updates and refactoring. When converting existing UI markup or custom components, prefer daisyUI idioms (e.g., `menu` with `menu-title` for grouped lists, `details` element for collapsible groups, `join` component for button rows).
- **Risk-aware scope management**: When refactoring or migrating UI components, defer optional low-value cosmetic tweaks (e.g., button restyling, minor style adjustments) if they carry appearance or layout risk. Document deferred items in closed issues and ask before implementing. Prioritize core functionality and test stability over cosmetic polish.
- **Autonomous action boundaries**: Distinguish between "managed beads" (agent-owned issue categories that can be autonomously applied) and "human-owned trackers" (issues requiring explicit human approval before changes). Never apply changes to human-owned trackers without approval. For autonomous operations, hold at decision points when awaiting user feedback. If approval prompts consistently timeout, ask if well-evidenced recurring follow-ups should be autonomously applied on future runs.
- **Safety split for policy-relevant changes**: When implementing changes that relax UI gates, access restrictions, or other policy/security decisions, separate implementation + testing from the commit step. If an approval prompt times out but the user says to start working, implement and test the fix without committing. Then ask the user how they want the work split across commits, keeping the policy decision separate from the technical decision. This prevents bundling irreversible policy changes with technical implementation.
- **Conversation deduplication and ownership**: When multiple conversations could act on the same work item (same PR, branch, or beads issue), respect ownership boundaries. Route fixes or follow-up actions to already-active owning conversations rather than spawning competing fix conversations. This prevents concurrent pushes to the same branch and resource conflicts between agents.
- **Explicit commit approval required**: NEVER commit code without explicit user instruction to do so. Agents must ask for approval before committing, even if the code is correct and all tests pass. Do not commit at the end of a task unless the user explicitly asks for it.
- **Explicit beads issue closure**: NEVER close a beads issue without explicit user instruction, even after implementing the work. The user must explicitly approve closing the issue.
- **Progress tracking with bd comment**: Use `bd comment <id>` to record work progress on beads issues without closing them. This allows intermediate progress updates while awaiting user direction on commits/closure.
- **Conflict-free increment strategy**: When working on concurrent epics across conversations, prioritize non-blocking, conflict-free increments that don't require editing files owned by other active conversations. Use optional component props with graceful degradation (fallback to plain text input) to unblock self-contained work and enable parallel progress on related features without merge conflicts.
- **Compile-time interface assertions**: Verify that concrete types satisfy interface contracts using compile-time assertions (e.g., `var _ conversation.SharedProcess = (*SharedACPProcess)(nil)`). Place these assertions in the same file as the implementation to catch breaking changes at compile time.
- **Dependency analysis before delegation**: Before delegating refactoring work to sub-agents, perform thorough dependency analysis to identify all affected call sites, imports, and type references. Derive a fully-specified plan from this analysis, then delegate with explicit instructions. This prevents rework and ensures completeness.
- **Independent verification checklist**: After receiving delegated work, independently verify by running: `go build ./...`, `go vet`, relevant test suites, checking for deprecated patterns/aliases, and confirming no import cycles. Run each check and report all results before considering work complete.
- **Scope decisions documented on beads**: When deferring interfaces or components to future increments, document the orchestration rationale directly on the beads issue (e.g., "ProcessManager/EventsBroadcaster deferred to .1.7 because they're consumed only by SessionManager, not BackgroundSession — creating them now would be dead code"). This helps the next increment understand the design intent.
- **UI transparency for periodic configuration**: Always display the prompt that will actually execute in a periodic conversation's selector (not empty placeholder). Free-text periodic prompts should show a preview or indicator; only show "Select a prompt…" for genuinely unconfigured conversations.
<!-- END USER PREFERENCES -->
