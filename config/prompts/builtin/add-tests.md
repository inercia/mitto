---
name: "Add tests"
description: "Write comprehensive tests for new or modified code"
backgroundColor: "#FFE0B2"
---

Write comprehensive tests for the code we created or modified.

### Include tests for:

1. **Happy path**: Normal expected usage
2. **Edge cases**: Empty inputs, boundary values, maximum sizes
3. **Error cases**: Invalid inputs, missing data, permission errors
4. **Concurrency**: Race conditions, deadlocks (if applicable)
5. **Integration**: Interaction with dependencies

### Test structure:
- Use descriptive test names that explain the scenario
- Follow Arrange-Act-Assert pattern
- One assertion per test when possible
- Use parameterized/data-driven tests for multiple similar cases
- Mock external dependencies appropriately
- Follow the project's existing testing conventions and patterns

### After writing:
- Run the tests and verify they pass
- Check coverage of the new/modified code

