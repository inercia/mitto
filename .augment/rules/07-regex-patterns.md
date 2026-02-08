---
description: Regular expression patterns, text matching, URL/path detection, and pattern-based HTML processing
globs:
  - "internal/conversion/**/*.go"
  - "internal/msghooks/**/*.go"
  - "web/static/lib.js"
keywords:
  - regex
  - regexp
  - pattern
  - matching
  - FindAllStringSubmatchIndex
  - MatchString
  - URL detection
  - path detection
  - linkify
---

# Regular Expression Patterns

## Go Regex Patterns

### Package-Level Patterns

Define regex patterns at package level for reuse and compilation efficiency:

```go
// Compile once at package initialization
var (
    urlPattern = regexp.MustCompile(
        `\b((?:https?://|ftp://|mailto:)[^\s<>"\[\]{}|\\^` + "`" + `]+)`,
    )
    filePathPattern = regexp.MustCompile(
        `(?:^|[\s>"\x60])` +
        `(\.{1,2}/[^\s<>"'\x60]+|/[^\s<>"'\x60]+|[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_.\-]+)+)` +
        `(?:[\s<>"'\x60]|$)`,
    )
)
```

### Processing HTML with Regex

When processing HTML content with regex:

1. **Find skip regions first** (code blocks, pre tags, anchor tags):
   ```go
   preRegions := fl.findPreRegions(html)
   ```

2. **Process matches in reverse order** to preserve indices:
   ```go
   matches := pattern.FindAllStringSubmatchIndex(html, -1)
   result := html
   for i := len(matches) - 1; i >= 0; i-- {
       match := matches[i]
       // Extract indices
       fullStart, fullEnd := match[0], match[1]
       contentStart, contentEnd := match[2], match[3]
       
       // Skip if in skip region
       if fl.isInSkipRegion(fullStart, fullEnd, skipRegions) {
           continue
       }
       
       // Process and replace
       replacement := processContent(html[contentStart:contentEnd])
       result = result[:fullStart] + replacement + result[fullEnd:]
   }
   ```

3. **Use capturing groups** for extraction:
   ```go
   // Pattern with capturing group
   pattern := regexp.MustCompile(`<code>([^<]+)</code>`)
   
   // Extract captured content
   match := pattern.FindStringSubmatch(html)
   if len(match) >= 2 {
       content := match[1]  // First capturing group
   }
   ```

### Escaping Special Characters

When building regex patterns with special characters:

```go
// Use backtick for literal backtick in pattern
var pattern = regexp.MustCompile(`[\s>"\x60]`)  // Matches backtick

// Or use string concatenation
var pattern = regexp.MustCompile(`[\s>"` + "`" + `]`)
```

## JavaScript Regex Patterns

### Global Patterns with State

Reset regex state when using global flag:

```javascript
const URL_PATTERN = /\b((?:https?:\/\/|ftp:\/\/|mailto:)[^\s<>"\[\]{}|\\^`]+)/gi;

function processText(text) {
  // Reset state before use
  URL_PATTERN.lastIndex = 0;
  
  let match;
  while ((match = URL_PATTERN.exec(text)) !== null) {
    // Process match
  }
}
```

### Testing Patterns

Always test regex patterns with edge cases:

```go
func TestPattern(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    bool
    }{
        {"valid URL", "https://example.com", true},
        {"URL with path", "https://example.com/path", true},
        {"partial URL", "example.com", false},
        {"URL with trailing period", "https://example.com.", true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := urlPattern.MatchString(tt.input)
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Common Patterns

### URL Detection
```go
// Matches http://, https://, ftp://, mailto:
`\b((?:https?://|ftp://|mailto:)[^\s<>"\[\]{}|\\^` + "`" + `]+)`
```

### File Path Detection
```go
// Matches relative and absolute paths
`(?:^|[\s>"\x60])` +
`(\.{1,2}/[^\s<>"'\x60]+|/[^\s<>"'\x60]+|[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_.\-]+)+)` +
`(?:[\s<>"'\x60]|$)`
```

### HTML Tag Matching
```go
// Match content inside tags
`(?s)<code[^>]*>.*?</code>`  // (?s) enables . to match newlines
`<pre[^>]*>.*?</pre>`
`<a[^>]*>.*?</a>`
```

## Performance Considerations

- Compile patterns once at package level
- Use `MatchString` for simple yes/no checks
- Use `FindStringSubmatch` when you need captured groups
- Use `FindAllStringSubmatchIndex` for multiple matches with positions
- Limit matches with `-1` parameter or slice results

