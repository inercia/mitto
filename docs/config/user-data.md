# User Data Configuration

User data allows you to attach custom metadata to conversations. This is useful for
tracking project-specific information like JIRA tickets, task descriptions, or any
other contextual data relevant to your work.

## Overview

User data consists of named attributes that you can define per workspace. Each
attribute has a name and a type that determines validation rules. The schema is
defined in your workspace's `.mittorc` file, and users can then set values for
these attributes on individual conversations.

## Configuration Schema

Define the allowed user data fields in your workspace `.mittorc` file:

```yaml
conversations:
  user_data:
    - name: "JIRA Ticket"
      type: string
    - name: "Documentation URL"
      type: url
    - name: "Task Description"
      type: string
```

### Field Properties

| Property | Required | Description                               |
| -------- | -------- | ----------------------------------------- |
| `name`   | Yes      | The display name of the attribute         |
| `type`   | No       | The attribute type (defaults to `string`) |

### Supported Types

| Type     | Description          | Validation                      |
| -------- | -------------------- | ------------------------------- |
| `string` | Plain text (default) | Any value accepted              |
| `url`    | A URL                | Must be a valid URL with scheme |

## Examples

### Basic String Attributes

Track simple text metadata:

```yaml
conversations:
  user_data:
    - name: "Project Name"
    - name: "Sprint"
    - name: "Priority"
```

Since `type` defaults to `string`, you can omit it for plain text fields.

### URL Attributes

Track links with validation:

```yaml
conversations:
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
conversations:
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
    - when: first
      position: prepend
      text: |
        This is the MyProject workspace.
        Follow our coding standards.
        ---

  # User data schema
  user_data:
    - name: "JIRA Ticket"
      type: url
    - name: "Feature Branch"
      type: string
    - name: "Sprint"
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

If you try to set an attribute that isn't in the schema, or provide an invalid
value for the type, the save will fail with a validation error.

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

- [Conversation Processing](conversations.md) - Message processing rules
- [Workspace Configuration](web/workspace.md) - Project-specific `.mittorc` files
- [Configuration Overview](overview.md) - Global configuration options
