---
name: "Run tests"
description: "Run the test suite and report results"
backgroundColor: "#FFE0B2"
---

Run the project's test suite and report results.

### 1. Discover and Run Tests

- Identify the test framework used (e.g., `go test`, `pytest`, `jest`, `cargo test`)
- Run all tests or tests related to recent changes
- Capture the full output

### 2. Analyze Results

If tests pass:
- Report success with a brief summary

If tests fail:
- For simple failures (typos, missing imports, obvious fixes):
  - Fix the issue immediately
  - Re-run the tests to verify the fix
  - Repeat until tests pass or failures are complex
- For complex failures:
  - Report them for manual review

### 3. Summary Table

Present a results table:

| Test Group | Passed | Failed | Skipped | Status |
|------------|--------|--------|---------|--------|
| unit       | 45     | 0      | 2       | ✅     |
| integration| 12     | 1      | 0       | ❌     |
| e2e        | 8      | 0      | 0       | ✅     |
| **Total**  | **65** | **1**  | **2**   | ❌     |

### 4. If Failures Remain

For each unresolved failure:
- **Test**: Name of the failing test
- **Error**: The error message
- **Cause**: Brief analysis of why it failed
- **Suggested fix**: What needs to change
