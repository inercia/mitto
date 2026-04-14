---
name: "Review"
description: "Review changes for quality and correctness"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

Review code across five axes: **correctness, readability, architecture, security, performance**.

**Approval standard:** Approve when a change improves overall code health, even if imperfect. Don't block because it isn't how you'd write it. The goal is continuous improvement, not perfection.

## Process

1. **Understand intent** — What is this change trying to do? What spec or task does it address?
2. **Read tests first** — Tests reveal intent and coverage gaps before you touch the implementation.
3. **Walk the implementation** — Evaluate each file against the five axes below.
4. **Label findings** — Every issue gets a severity:

| Label | Meaning |
|-------|---------|
| **Critical** | Blocks merge — security hole, data loss, broken functionality |
| *(no label)* | Must fix before merge |
| **Nit** | Optional — style, formatting |
| **Consider** | Suggestion worth thinking about |
| **FYI** | Context only, no action needed |

5. **Verify** — Tests pass, build succeeds, manual verification done if applicable.

## The Five Axes

### Correctness

- Matches spec/task requirements?
- Edge cases handled (null, empty, boundary)?
- Error paths handled, not just happy path?
- Tests exist and test the right things?
- Off-by-one errors, race conditions, state bugs?

### Readability

- Names are descriptive, consistent with project conventions?
- Control flow is straightforward (no deep nesting, clever tricks)?
- Could this be simpler? (1000 lines where 100 suffice is a failure)
- Abstractions earn their complexity? (Don't generalize until the third use)
- Dead code removed (unused vars, backwards-compat shims, `// removed` comments)?

### Architecture

- Follows existing patterns? If introducing a new one, is it justified?
- Clean module boundaries maintained?
- No code duplication that should be shared?
- Dependencies flow correctly (no cycles)?
- Abstraction level appropriate (not over-engineered, not too coupled)?

### Security

- User input validated and sanitized at boundaries?
- Secrets kept out of code, logs, and version control?
- Auth checks where needed?
- Queries parameterized (no string concatenation)?
- Outputs encoded (no XSS)?
- External data (APIs, user content, config) treated as untrusted?

### Performance

- No N+1 query patterns?
- No unbounded loops or unconstrained data fetching?
- Sync operations that should be async?
- Unnecessary re-renders in UI?
- Missing pagination on list endpoints?
- Large objects in hot paths?

## Honesty Rules

- **No rubber-stamping.** Every review must show evidence of actual review.
- **No softening real issues.** If it's a bug, say "bug" — not "minor concern."
- **Quantify when possible.** "This N+1 adds ~50ms per item" beats "could be slow."
- **No sycophancy.** If the approach has problems, say so and propose alternatives.
- **Defer gracefully.** If the author has full context and disagrees, accept it. Comment on code, not people.

## Dead Code

After refactoring, check for orphaned code. List it explicitly and ask before deleting:
```
DEAD CODE FOUND:
- formatLegacyDate() in utils/date.ts — replaced by formatDate()
- OldTaskCard component — replaced by TaskCard
→ Safe to remove?
```

## Dependencies

Before adding any dependency: Does the existing stack solve this? How large is it? Actively maintained? Known vulnerabilities? Compatible license? Prefer standard library over new deps.

## Output Format

```markdown
## Review: [title]

### Correctness

- [ ] Matches requirements
- [ ] Edge cases handled
- [ ] Error paths handled
- [ ] Tests adequate

### Readability

- [ ] Clear names, straightforward logic
- [ ] No unnecessary complexity

### Architecture

- [ ] Follows patterns, clean boundaries
- [ ] Appropriate abstraction

### Security

- [ ] Input validated, no secrets, no injection
- [ ] External data untrusted

### Performance

- [ ] No N+1, no unbounded ops, pagination present

### Verdict

- [ ] **Approve** — improves code health
- [ ] **Request changes** — issues listed above
```