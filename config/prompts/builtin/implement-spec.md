---
name: "Implement spec"
description: "Create a detailed implementation plan from a specification"
backgroundColor: "#FFECB3"
---

Read carefully the spec file we wrote.

## Phase 1: Understand the Specification

Before planning, ensure you fully understand:

1. **Core requirements**: What must be built?
2. **Success criteria**: How will we know it's complete?
3. **Constraints**: What limitations exist (tech stack, time, dependencies)?
4. **Scope boundaries**: What is explicitly out of scope?

If anything is unclear, ask clarifying questions before proceeding.

## Phase 2: Design the Architecture

Create a detailed blueprint for the implementation:

### Components to Consider

1. **Data models**: Entities, relationships, schemas
2. **APIs/Interfaces**: Endpoints, contracts, protocols
3. **Business logic**: Core algorithms, rules, workflows
4. **Error handling**: Failure modes, recovery strategies
5. **Security**: Authentication, authorization, data protection
6. **Testing strategy**: Unit, integration, E2E test approach
7. **Observability**: Logging, metrics, monitoring
8. **Versioning**: Backward compatibility considerations (if applicable)

### Dependency Analysis

- Identify dependencies between components
- Determine the optimal build order
- Note external dependencies that need to be installed/configured

**IMPORTANT**: Use the `think` tool (or any sequential/deep thinking tool available)
to reason deeply about the design.

## Phase 3: Create the Implementation Plan

Break down the work into small, iterative chunks:

### Iteration Guidelines

1. **First pass**: Identify major components and their order
2. **Second pass**: Break each component into smaller steps
3. **Third pass**: Review and ensure steps are:
   - Small enough to implement safely with strong testing
   - Large enough to make meaningful progress
   - Properly ordered based on dependencies

### Step Format

For each step, specify:

| # | Description | Files/Components | Dependencies | Verification |
|---|-------------|------------------|--------------|--------------|
| 1 | ... | ... | None | ... |
| 2 | ... | ... | Step 1 | ... |

### Principles

- Each step should be independently testable
- Prefer working software at every step over big-bang integration
- Include test writing as part of each step, not as a separate phase
- Consider rollback strategy for each step

**IMPORTANT**: Use the `todo` tool (or any task list tool available) to track
the implementation plan and mark progress.

## Phase 4: Begin Implementation

Present the plan and wait for approval before executing.

Once approved, work through the steps systematically:

1. Implement the step
2. Write/update tests
3. Verify the step works
4. Report progress
5. Move to the next step

If issues arise during implementation that affect the plan, stop and discuss
before continuing.

