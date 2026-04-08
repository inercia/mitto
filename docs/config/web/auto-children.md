# Auto-Create Children Conversations

The **Auto-Children** feature automatically spawns child conversations whenever a new top-level conversation is created in a workspace. This is useful for setting up a team of specialized AI helpers that are always available alongside your primary conversation.

## The Smart Model + Fast Helpers Pattern

The most powerful use case is pairing a powerful-but-slow model with lightweight helpers:

- **Parent workspace**: uses a capable model (e.g., Opus) — orchestrates and reasons
- **Auto-child "coder"**: uses a fast coding model (e.g., Sonnet) — executes implementation tasks
- **Auto-child "shell runner"**: uses a lightweight model (e.g., Haiku) — runs shell commands, quick lookups

When you create a new conversation in the parent workspace, Mitto automatically creates the coder and shell runner conversations as children. The parent can then delegate tasks to them using the MCP `mitto_children_tasks_wait` tool.

```
Parent (Opus)
├── coder (Sonnet)       ← fast, good at writing code
└── shell runner (Haiku) ← very fast, cheap for simple commands
```

## Configuration

Auto-children are configured in `workspaces.json` (located in the Mitto data directory):

| Platform  | Path                                                          |
| --------- | ------------------------------------------------------------- |
| macOS     | `~/Library/Application Support/Mitto/workspaces.json`        |
| Linux     | `~/.local/share/mitto/workspaces.json`                        |

### Example workspaces.json

```json
{
  "workspaces": [
    {
      "uuid": "ws-opus",
      "name": "mitto",
      "acp_server": "Auggie (Opus 4.5)",
      "working_dir": "/path/to/project",
      "auto_children": [
        { "title": "coder",        "target_workspace_uuid": "ws-sonnet" },
        { "title": "shell runner", "target_workspace_uuid": "ws-haiku"  }
      ]
    },
    {
      "uuid": "ws-sonnet",
      "name": "Sonnet helper",
      "acp_server": "Auggie (Sonnet 4.6)",
      "working_dir": "/path/to/project"
    },
    {
      "uuid": "ws-haiku",
      "name": "Haiku helper",
      "acp_server": "Auggie (Haiku 4.6)",
      "working_dir": "/path/to/project"
    }
  ]
}
```

### AutoChild Fields

| Field                  | Required | Description                                                                         |
| ---------------------- | -------- | ----------------------------------------------------------------------------------- |
| `title`                | Yes      | Name displayed for the child conversation                                           |
| `target_workspace_uuid`| No       | UUID of the workspace to use for the child. Defaults to the parent's own workspace. |

### Constraints

- **Maximum 5 auto-children** per workspace.
- Auto-children are only created for **top-level conversations** (conversations without a parent). This prevents infinite recursion when MCP tools create their own child sessions.
- The `uuid` field is required on each workspace when using `target_workspace_uuid` references.

## UI Configuration

You can configure auto-children through **Settings > Workspaces** without editing `workspaces.json` directly:

1. Open Settings (gear icon in the sidebar)
2. Select the **Workspaces** tab
3. Click on a workspace to expand its settings
4. Under **Auto-created children**, click **Add child**
5. Set the child title and choose a target workspace from the dropdown
6. Save — changes take effect immediately for new conversations

## Behavior

### Creation

When a new top-level conversation is created in a workspace with `auto_children` configured:

1. Mitto creates each child session in the data store with `is_auto_child: true`
2. The child ACP process is started immediately (using the target workspace's ACP server)
3. Each child inherits the **parent's working directory** (not the target workspace's directory)
4. The frontend is notified via WebSocket (`session_created` broadcast) for each child

Children are created **asynchronously** — failures are logged but do not block parent creation.

### Deletion (Cascade)

Auto-children are **cascade-deleted** when their parent is deleted:

```
Delete parent
  ├── ACP processes stopped (parent + all auto-children)
  ├── Auto-children deleted from store (recursive)
  └── Frontend notified for each deleted session
```

> **Note:** Conversations created by MCP tools (`mitto_conversation_new`) are treated differently. They are **orphaned** (their `parent_session_id` is cleared) rather than deleted when the parent is removed.

### Comparison: Auto-Children vs MCP-Created Children

| Aspect               | Auto-children (`is_auto_child: true`) | MCP-created children (`is_auto_child: false`) |
| -------------------- | ------------------------------------- | ---------------------------------------------- |
| Created when         | New top-level conversation            | Via `mitto_conversation_new` MCP tool          |
| On parent delete     | Cascade-deleted                       | Orphaned (parent link cleared)                 |
| Working directory    | Inherited from parent                 | Specified by MCP call                          |
| ACP server           | From target workspace                 | From target workspace or MCP call              |

## Use Cases

### Delegating Implementation to a Fast Model

The parent Opus conversation plans and reasons; child Sonnet executes:

```
Parent (Opus): "Analyze this codebase and plan a refactor"
  → delegates to → coder (Sonnet): "Implement the refactor in file X"
  → delegates to → shell runner (Haiku): "Run the tests"
```

### Parallel Task Execution

Use `mitto_children_tasks_wait` to run tasks in parallel across children:

```
Parent starts tasks in child-1 and child-2 simultaneously,
then waits for both to complete before proceeding.
```

### Specialized Helpers

Configure children with different roles:
- One child for writing tests
- One child for documentation
- One child for infrastructure/devops tasks

## Related Documentation

- [Workspace Configuration](workspace.md) — `.mittorc` workspace files
- [MCP Server](../../config/mcp.md) — MCP tools for multi-agent coordination
- [Session Management](../../devel/session-management.md) — How children are stored and managed
