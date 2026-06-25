# User Data Configuration

User data allows you to attach custom metadata to conversations. This is useful for
tracking project-specific information like JIRA tickets, task descriptions, or any
other contextual data relevant to your work.

## Configuration in the UI

User data schema is defined in the **Workspaces → Metadata** tab, under **User Data Schema**:

![Workspaces — Metadata tab](screenshots/04-workspace-metadata.png)

From this tab you can:

- **Add fields** with the **+ Add Field** button
- Set each field's **name**, **type** (string, URL, or filename), and **description**
- Remove fields you no longer need

Once a schema is defined, users can set values on individual conversations via the conversation properties panel.

---

## YAML Configuration

### Overview

User data consists of named attributes that you can define per workspace. Each
attribute has a name and a type that determines validation rules. The schema is
defined in your workspace's `.mittorc` file, and users can then set values for
these attributes on individual conversations.

### Configuration Schema

Define the allowed user data fields in your workspace `.mittorc` file:

```yaml
metadata:
  user_data:
    - name: "JIRA Ticket"
      description: "The JIRA ticket for the current task"
      type: string
    - name: "Documentation URL"
      description: "Link to related documentation"
      type: url
    - name: "Task Description"
      type: string
```

### Field Properties

| Property      | Required | Description                                                       |
| ------------- | -------- | ----------------------------------------------------------------- |
| `name`        | Yes      | The display name of the attribute                                 |
| `description` | No       | Human-readable description (shown as tooltip/placeholder in UI)   |
| `type`        | No       | The attribute type (defaults to `string`)                         |

### Supported Types

| Type       | Description                    | Validation                                                      |
| ---------- | ------------------------------ | --------------------------------------------------------------- |
| `string`   | Plain text (default)           | Any value accepted                                              |
| `url`      | A URL                          | Must be a valid URL with scheme                                 |
| `filename` | A workspace-relative file path | Must point to an existing, readable file (not a directory)      |

`filename` values are rendered as clickable links in the UI. Clicking opens the file in Mitto's internal viewer.

A `filename` value may be an absolute path or a path relative to the conversation's
working directory. When saving, the file must exist, be readable by the Mitto
server process, and not be a directory; otherwise the save is rejected. An empty
value is allowed (the field is unset).

## Examples

### Basic String Attributes

Track simple text metadata:

```yaml
metadata:
  user_data:
    - name: "Project Name"
    - name: "Sprint"
    - name: "Priority"
```

Since `type` defaults to `string`, you can omit it for plain text fields.

### URL Attributes

Track links with validation:

```yaml
metadata:
  user_data:
    - name: "JIRA Ticket"
      type: url
    - name: "Design Doc"
      type: url
    - name: "PR Link"
      type: url
```

URL attributes must include a scheme (e.g., `https://`). Invalid URLs will be
rejected when saving.

**Valid URLs:**

- `https://jira.example.com/browse/PROJ-123`
- `http://localhost:3000/docs`

**Invalid URLs:**

- `jira.example.com/browse/PROJ-123` (missing scheme)
- `not a url`

### Mixed Types

Combine different attribute types:

```yaml
metadata:
  user_data:
    - name: "Task"
      type: string
    - name: "JIRA"
      type: url
    - name: "Notes"
      type: string
    - name: "Related PR"
      type: url
```

### Complete Workspace Configuration

A full `.mittorc` example with user data and other settings:

```yaml
# Custom prompts for this workspace
prompts:
  - name: "Run Tests"
    prompt: "Run all tests and report results"

# Conversation settings
conversations:
  # Message processing
  processing:
    - when:
        on: userPrompt
        match: first
      mutate: prepend
      text: |
        This is the MyProject workspace.
        Follow our coding standards.
        ---

# Workspace metadata and user data schema
metadata:
  group: "MyTeam"
  user_data:
    - name: "JIRA Ticket"
      description: "The JIRA ticket associated with the current work"
      type: url
    - name: "Feature Branch"
      description: "The git feature branch name"
      type: string
    - name: "Sprint"
      description: "The current sprint"
      type: string
```

## Using User Data

Once configured, user data can be viewed and edited in the web interface:

1. **Open a conversation** in the web interface
2. **Click the conversation title** or the **pencil icon** in the sidebar
3. **The Properties panel** opens on the right side
4. **Edit the attribute values** and click Save

User data is stored per-conversation and persists across sessions.

## Validation Behavior

- **No schema defined**: No user data attributes are allowed
- **Schema defined**: Only attributes listed in the schema are allowed
- **Empty values**: Always allowed (you can clear any attribute)
- **Type validation**: Applied when saving (e.g., URLs must be valid)
- **Filename validation**: A `filename` value must resolve (absolute, or relative
  to the conversation's working directory) to an existing, readable file that is
  not a directory

If you try to set an attribute that isn't in the schema, or provide an invalid
value for the type, the save will fail with a validation error.

## Accessing User Data in Prompts

User data fields are available in prompt bodies (Go templates) and in `enabledWhen`
CEL expressions as a structured `name → value` map. This lets a prompt branch on a
single field — for example, set it if unset, otherwise continue.

In a prompt body (template), use the `UserData` function or the `.UserData` map:

```yaml
prompt: |
  {{ if UserData "JIRA Ticket" }}
  Continue work on {{ UserData "JIRA Ticket" }}.
  {{ else }}
  No JIRA ticket is set yet. Determine it from the conversation and call
  mitto_conversation_update with user_data to set "JIRA Ticket", then proceed.
  {{ end }}
```

`{{ UserData "NAME" }}` returns the field value, or `""` when unset (it handles
names with spaces). `{{ index .UserData "NAME" }}` accesses the same map directly.

In `enabledWhen` (menu-time visibility), reference the `UserData` map:

```yaml
enabledWhen: '"JIRA Ticket" in UserData && UserData["JIRA Ticket"] != ""'
```

The full JSON blob is still available via `{{ .Session.UserDataJSON }}` (and the
legacy `@mitto:user_data` placeholder) when you need every attribute at once.

See [Prompt Configuration → Go Template Syntax](prompts.md#go-template-syntax-in-prompts)
for the complete template reference.

## Storage

User data is stored in `user-data.json` within each session's directory:

```
~/Library/Application Support/Mitto/sessions/<session-id>/user-data.json
```

The file format:

```json
{
  "attributes": [
    { "name": "JIRA Ticket", "value": "https://jira.example.com/PROJ-123" },
    { "name": "Sprint", "value": "Sprint 42" }
  ]
}
```

## Related Documentation

- [Conversation Settings](conversations.md) - Auto-approve, auto-archive, external images
- [Processors](processors.md) - Message transformation (text, command, prompt modes)
- [Workspace Configuration](workspace.md) - Project-specific `.mittorc` files
- [Configuration Overview](overview.md) - Global configuration options
