# Mixed Formatting Patterns

This fixture tests various formatting patterns that might cause issues.

## Bold and Italic

This has **bold text** and *italic text* and ***bold italic***.

## Inline Code

Use `fmt.Println()` to print and `os.Exit(1)` to exit.

## Nested Formatting

- **Bold with `code` inside**
- *Italic with `code` inside*
- `code with **bold** inside` (should not render bold)

## Links

Check out [this link](https://example.com) and [another **bold** link](https://example.com).

