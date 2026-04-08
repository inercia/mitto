---
name: "Run tests"
description: "Run the test suite and report results"
group: "Testing"
backgroundColor: "#FFE0B2"
---

Run the project's test suite and report results.

### 1. Discover and Run

Identify test framework from project config:
- Go: `go test` | Python: `pytest`, `unittest` | JS/TS: `jest`, `vitest` | Rust: `cargo test` | Java: `mvn test`
- Check Makefile targets, package.json scripts

Run all tests or tests related to recent changes.

### 2. Analyze

Pass: report success.

Fail:

- Simple failures (typos, missing imports): fix immediately, re-run, repeat
- Complex failures: report for manual review

### 3. Summary



| Test Group | Passed | Failed | Skipped | Status |
|------------|--------|--------|---------|--------|
| unit       | 45     | 0      | 2       | ✅     |
| integration| 12     | 1      | 0       | ❌     |
| **Total**  | **57** | **1**  | **2**   | ❌     |



### 4. Unresolved Failures

Per failure: **Test**, **Error**, **Cause**, **Suggested fix**
