# Table with Inline Formatting

This fixture tests a table with bold and code formatting inside cells.

| Feature | Status | Description |
|---------|--------|-------------|
| **Bold text** | `code` | Regular text |
| Normal | **Multi word bold** | `inline_code` |
| `code_first` | Normal | **bold_last** |

The table above should be rendered as a single unit.

