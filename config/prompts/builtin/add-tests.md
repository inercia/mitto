---
name: "Add tests"
description: "Write comprehensive tests for new or modified code"
group: "Testing"
backgroundColor: "#FFE0B2"
---

<investigate_before_answering>
Read the modified code and existing test files to understand testing conventions.
</investigate_before_answering>

<task>
Write comprehensive tests for the code we created or modified.
</task>

<scope>
Test code that was actually changed. Verify real behavior, not implementation
details. Use general assertions, not hard-coded values.
</scope>

<instructions>

### Coverage:

1. Happy path
2. Edge cases (empty inputs, boundaries, max sizes)
3. Error cases (invalid inputs, missing data, permission errors)
4. Concurrency (race conditions, deadlocks — if applicable)
5. Integration with dependencies

### Structure:

- Descriptive test names explaining the scenario
- Arrange-Act-Assert pattern
- One assertion per test when possible
- Parameterized tests for similar cases
- Mock external dependencies
- Follow project's existing conventions

### After writing:

- Run tests, verify they pass
- Check coverage of new/modified code

</instructions>
