# Long Code Block

This fixture tests a long code block that might trigger buffer size limits.

```go
package main

import (
    "fmt"
    "time"
)

// LongFunction demonstrates a function with many lines
func LongFunction() error {
    // Step 1: Initialize
    fmt.Println("Initializing...")
    time.Sleep(100 * time.Millisecond)

    // Step 2: Process
    for i := 0; i < 10; i++ {
        fmt.Printf("Processing item %d\n", i)
        if i == 5 {
            fmt.Println("Halfway done!")
        }
    }

    // Step 3: Validate
    if err := validate(); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }

    // Step 4: Finalize
    fmt.Println("Finalizing...")
    cleanup()

    // Step 5: Report
    fmt.Println("Done!")
    return nil
}

func validate() error {
    return nil
}

func cleanup() {
    // cleanup logic here
}

func main() {
    if err := LongFunction(); err != nil {
        fmt.Printf("Error: %v\n", err)
    }
}
```

The code block above should be rendered as a single unit.

