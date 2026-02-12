# Code Block with Simulated Pause

This fixture tests a code block that might have a pause during streaming.
The code block should NOT be split even if there's a delay.

```go
// Set hard inactivity timeout that forces flush regardless of block state.
// This ensures content is displayed even if the agent stops mid-block.
if mb.inactivityTimer == nil {
    mb.inactivityTimer = time.AfterFunc(inactivityFlushTimeout, func() {
        mb.mu.Lock()
        defer mb.mu.Unlock()
        if mb.buffer.Len() > 0 {
            content := mb.buffer.String()
            // Don't flush if we have unmatched inline formatting
            if conversion.HasUnmatchedInlineFormatting(content) {
                return
            }
            mb.flushLocked()
        }
    })
}
```

The code block above should be rendered as a single unit.

