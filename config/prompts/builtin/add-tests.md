---
name: "Add tests"
description: "Write comprehensive tests for new or modified code"
group: "Testing"
backgroundColor: "#FFE0B2"
---

<investigate_before_answering>
Before writing any tests, read the code that was created or modified. Understand its
behavior, inputs, outputs, and edge cases by examining the actual implementation.
Also review existing test files to understand the project's testing conventions and patterns.
</investigate_before_answering>

<task>
Write comprehensive tests for the code we created or modified.
</task>

<scope>
Focus tests on the code that was actually changed. Write tests that verify real behavior,
not implementation details. Implement general assertions that work for all valid inputs,
not just specific hard-coded values.
</scope>

<instructions>

### Include tests for:

1. **Happy path**: Normal expected usage
2. **Edge cases**: Empty inputs, boundary values, maximum sizes
3. **Error cases**: Invalid inputs, missing data, permission errors
4. **Concurrency**: Race conditions, deadlocks (if applicable)
5. **Integration**: Interaction with dependencies

### Test structure:

- Use descriptive test names that explain the scenario
- Follow the Arrange-Act-Assert pattern
- One assertion per test when possible
- Use parameterized/data-driven tests for multiple similar cases
- Mock external dependencies appropriately
- Follow the project's existing testing conventions and patterns

### After writing:

- Run the tests and verify they pass
- Check coverage of the new/modified code

</instructions>
