# Workspace Configuration

Workspaces connect **project folders** to **ACP servers**. Each workspace defines which AI agent handles a particular project, and carries folder-specific settings like prompts, processors, and runner restrictions.

## Configuration in the UI

### Managing Workspaces

Open the **Workspaces** dialog by clicking the 📁 icon in the sidebar footer:

![Workspaces dialog](screenshots/03-workspaces-overview.png)

The left panel lists your workspaces grouped by folder. Each folder can have multiple ACP server entries (e.g., one with Claude Code, another with Auggie for the same project).

**Toolbar buttons** at the bottom of the left panel:

| Button | Action |
|--------|--------|
| 📁 **Add Folder** | Create a new workspace — you'll be prompted to select a folder |
| 🗑️ **Delete** | Remove the selected ACP server entry |
| 📋 **Duplicate** | Clone the selected workspace with a new UUID |
| 🖥️ **Add Server** | Add another ACP server to the currently selected folder |

### Editing Workspace Settings

Click a **folder name** (e.g., "Mitto") to access folder-level settings. Click an **ACP server entry** under a folder to access workspace-level settings:

![Workspace — General tab](screenshots/04-workspace-general.png)

The right panel provides these tabs:

| Tab | Screenshot | What it configures |
|-----|------------|--------------------|
| **General** | ![](screenshots/04-workspace-general.png) | ACP server, auxiliary server, runner, auto-approve |
| **Metadata** | ![](screenshots/04-workspace-metadata.png) | Display name, description, URL, user data schema |
| **Prompts** | ![](screenshots/04-workspace-prompts.png) | Quick-action prompts (enable/disable, add, edit) |
| **Processors** | ![](screenshots/04-workspace-processors.png) | Message processors (enable/disable per workspace) |
| **Children** | ![](screenshots/04-workspace-children.png) | Auto-spawn child conversations |
| **Runner** | — | Restricted execution sandbox settings |
| **MCP** | — | MCP server configuration and installation |

---

## YAML Configuration (`.mittorc`)

For advanced users or automation, workspace settings can also be defined via a `.mittorc` YAML file in the project root:

```
my-project/
├── .mittorc          # Workspace-specific configuration
├── src/
├── tests/
└── ...
```

The file is automatically loaded when you open a workspace in Mitto and reloaded every 30 seconds to pick up changes.

### Configuration Hierarchy

| Level         | File                            | Scope                          |
| ------------- | ------------------------------- | ------------------------------ |
| **Global**    | `~/.mittorc` or `settings.json` | Applies to all workspaces      |
| **Workspace** | `<project>/.mittorc`            | Applies only to that workspace |

### Supported Sections

| Section           | Description                                              | Details |
| ----------------- | -------------------------------------------------------- | ------- |
| `prompts`         | Quick-action prompts shown in the chat interface         | [Prompts](prompts.md) |
| `conversations`   | Inline text-mode processors                              | [Processors](processors.md#inline-processors-in-mittorc) |
| `processors_dirs` | Additional processor directories                         | [Processors](processors.md#workspace-local-processors) |
| `metadata`        | Display name, description, URL, user data schema         | [User Data](user-data.md) |

> **Note**: Sections like `acp`, `web`, and `ui` are ignored in workspace files — these can only be configured globally.

### Complete `.mittorc` Example

```yaml
# Workspace-specific configuration for MyProject
# Place this file at: my-project/.mittorc

# Workspace metadata
metadata:
  description: "Node.js API with TypeScript"
  url: "https://github.com/myorg/myproject"
  user_data:
    - name: "JIRA Ticket"
      type: string

# Quick-action prompts for this project
prompts:
  - name: "Run Tests"
    backgroundColor: "#BBDEFB"
    prompt: "Run the test suite with: npm test"

  - name: "Build & Deploy"
    backgroundColor: "#E8F5E9"
    prompt: "Build and deploy: npm run build && npm run deploy"

# Inline text-mode processors
conversations:
  processing:
    processors:
      - when: first
        position: prepend
        text: |
          You are working on MyProject, a Node.js application.
          Follow TypeScript strict mode and ESLint rules.
          ---
```

## Auto-Created Children

Workspaces can automatically spawn child conversations when a new top-level conversation is created. This is configured through the **Children** tab in the UI or via the `auto_children` field in workspaces.json.

See **[Auto-Create Children Conversations](auto-children.md)** for details and the "smart model + fast helpers" pattern.

## Related Documentation

- [Prompts](prompts.md) - Quick actions and predefined prompts
- [Processors](processors.md) - Message transformation (text, command, prompt modes)
- [User Data](user-data.md) - Custom metadata for conversations
- [Conversation Settings](conversations.md) - Auto-approve, auto-archive, external images
- [Auto-Create Children](auto-children.md) - Auto-spawn helper conversations
- [Configuration Overview](overview.md) - Global configuration options
