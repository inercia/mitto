---
description: CLI commands with Cobra, signal handling, user feedback with emoji, and multi-workspace usage
globs:
  - "internal/cmd/**/*"
  - "cmd/mitto/**/*"
---

# CLI UX Patterns

## Cobra Command Structure

```go
var cliCmd = &cobra.Command{
    Use:   "cli",
    Short: "One-line description",
    Long:  `Multi-line description with examples...`,
    RunE:  runCLI,  // Use RunE for error returns
}

func init() {
    rootCmd.AddCommand(cliCmd)
    cliCmd.Flags().StringVar(&flagVar, "flag", "", "Description")
}
```

## User Feedback

```go
// Use emoji prefixes for visual clarity
fmt.Printf("🚀 Starting ACP server: %s\n", server.Name)
fmt.Printf("✅ Connected (protocol v%v)\n", version)
fmt.Printf("🔐 Permission requested: %s\n", title)
fmt.Println("👋 Shutting down...")

// Suppress noise in non-interactive mode
if !isOnceMode || debug {
    fmt.Printf("🚀 Starting...\n")
}
```

## Signal Handling

```go
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigChan
    cancel()  // Cancel context
}()
```

## CLI Configuration Loading

```bash
# Default: Load from MITTO_DIR/settings.json (auto-creates if missing)
mitto cli

# Override with specific config file (YAML or JSON, detected by extension)
mitto cli --config /path/to/config.yaml
mitto cli --config /path/to/config.json
```

## Multi-Workspace CLI Usage

```bash
# Single workspace (default)
mitto web

# Multiple workspaces with --dir flag
mitto web --dir /path/to/project1 --dir /path/to/project2

# Specify ACP server per workspace (server:path syntax)
mitto web --dir auggie:/path/to/project1 --dir claude-code:/path/to/project2

# Mix default and explicit servers
mitto web --dir /path/to/project1 --dir claude-code:/path/to/project2
```
