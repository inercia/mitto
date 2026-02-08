# go-restricted-runner Library Updates

## Summary

The go-restricted-runner library has been updated with the `RunWithPipes()` method, which enables interactive process communication with restricted processes. This is essential for Mitto's ACP agent integration.

## What Changed

### New Method: RunWithPipes()

The library now provides a `RunWithPipes()` method that returns stdin/stdout/stderr pipes for interactive communication:

```go
stdin, stdout, stderr, wait, err := runner.RunWithPipes(
    ctx,
    command,
    args,
    env,
    params,
)
```

**Returns**:

- `stdin`: io.WriteCloser for sending input to the process
- `stdout`: io.ReadCloser for reading process output
- `stderr`: io.ReadCloser for reading process errors
- `wait()`: Function to wait for process completion and cleanup
- `err`: Any error during process startup

### Comparison: Run() vs RunWithPipes()

| Feature           | Run()                | RunWithPipes()              |
| ----------------- | -------------------- | --------------------------- |
| **Use case**      | One-shot commands    | Interactive processes       |
| **Returns**       | Output string        | Pipes (stdin/stdout/stderr) |
| **Execution**     | Waits for completion | Returns immediately         |
| **Communication** | None                 | Bidirectional               |
| **Cleanup**       | Automatic            | Manual (call wait())        |

**When to use Run()**:

- Execute a command and get output after completion
- Simple, non-interactive commands
- No need for streaming or real-time communication

**When to use RunWithPipes()**:

- Interactive processes (REPLs, shells)
- Long-running processes with bidirectional communication
- Streaming data to/from the process
- **ACP agent communication** (Mitto's use case)

## Usage Examples

### Example 1: Simple Interactive Process

```go
logger, _ := common.NewLogger("", "", common.LogLevelInfo, false)
r, _ := runner.New(runner.TypeExec, runner.Options{}, logger)

ctx := context.Background()
stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "cat", nil, nil, nil)
if err != nil {
    log.Fatal(err)
}

// Send input
fmt.Fprintln(stdin, "Hello, World!")
stdin.Close()

// Read output
output, _ := io.ReadAll(stdout)
fmt.Println(string(output))

// Cleanup
wait()
```

### Example 2: Python REPL

```go
stdin, stdout, stderr, wait, err := r.RunWithPipes(
    ctx,
    "python3",
    []string{"-i"},  // Interactive mode
    nil,
    nil,
)

// Send Python commands
fmt.Fprintln(stdin, "x = 10")
fmt.Fprintln(stdin, "y = 20")
fmt.Fprintln(stdin, "print(x + y)")
fmt.Fprintln(stdin, "exit()")
stdin.Close()

// Read output
output, _ := io.ReadAll(stdout)
wait()
```

### Example 3: ACP Agent (Mitto's Use Case)

```go
// Start agent with restrictions
stdin, stdout, stderr, wait, err := runner.RunWithPipes(
    ctx,
    "auggie",
    []string{"--acp"},
    env,
    nil,
)

// Set up ACP client
acpClient := acp.NewClient(stdin, stdout)

// Monitor stderr
go func() {
    scanner := bufio.NewScanner(stderr)
    for scanner.Scan() {
        logger.Debug("agent: %s", scanner.Text())
    }
}()

// Send ACP messages
acpClient.SendRequest(...)

// Cleanup
stdin.Close()
wait()
```

## Important Notes

### Resource Management

1. **Always close stdin** when done writing to signal EOF
2. **Always call wait()** to clean up resources, even if you don't care about the exit status
3. **Read from stdout/stderr** before or after calling wait() - both work
4. **Context cancellation** will kill the process immediately

### Restriction Enforcement

All restrictions still apply when using RunWithPipes():

- Path restrictions (allow_read_folders, allow_write_folders, deny_folders)
- Network restrictions (allow_networking)
- Docker isolation
- Sandbox profiles (sandbox-exec, firejail)

### Runner Type Support

RunWithPipes() works with all runner types:

- **exec**: Direct execution with pipes
- **sandbox-exec**: macOS sandbox with pipes
- **firejail**: Linux namespace isolation with pipes
- **docker**: Container execution with `docker exec -i`

## Impact on Mitto

### What This Enables

1. **ACP Communication**: Mitto can now use restricted runners for ACP agents
2. **Bidirectional JSON-RPC**: Full support for ACP protocol over stdio
3. **Real-time Streaming**: Agent responses stream back to Mitto in real-time
4. **Proper Cleanup**: wait() ensures processes are cleaned up correctly

### Implementation Changes Needed

The original implementation plan assumed we would need to extend go-restricted-runner. Since RunWithPipes() is now available, we can:

1. ✅ Use RunWithPipes() directly (no need to extend the library)
2. ✅ All runner types supported out of the box
3. ✅ Proper resource cleanup via wait()
4. ✅ Context cancellation works correctly

### Updated Integration Steps

1. Import go-restricted-runner (latest version)
2. Create runner with restrictions from config
3. Call RunWithPipes() to start the agent
4. Pass stdin/stdout to ACP client
5. Monitor stderr for agent logs
6. Call wait() when session ends

## Documentation Updates

### User-Facing Changes

Updated `docs/config/restricted.md`:

- Added explanation of Run() vs RunWithPipes()
- Documented that Mitto uses RunWithPipes() for ACP communication
- Added implementation details section
- Explained how restrictions are enforced

### Developer Documentation

Updated `docs/devel/restricted-runner-integration.md`:

- Marked Challenge 1 as SOLVED
- Added RunWithPipes() usage examples
- Updated library version information
- Removed need to extend go-restricted-runner

## Next Steps

1. Update go.mod to use latest go-restricted-runner
2. Implement runner package in internal/runner/
3. Integrate with ACP client startup
4. Add tests for RunWithPipes() integration
5. Update configuration loading to support restricted_runner

## References

- Library: https://github.com/inercia/go-restricted-runner
- README: https://github.com/inercia/go-restricted-runner/blob/main/README.md
- User docs: docs/config/restricted.md
- Integration plan: docs/devel/restricted-runner-integration.md
