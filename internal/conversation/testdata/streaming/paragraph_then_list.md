# Paragraph Followed by List

The test passes now. The key insight is that:

1. The list is NOT flushed mid-stream when the bold is unmatched (the bold text spans across a blank line), but that's a limitation of the markdown parser, not our buffering logic.
2. The tool call is buffered (because `inList` is still true)
3. Everything is flushed together at the end when `FlushMarkdown()` is called

The final HTML still shows the issue because the markdown is malformed.

