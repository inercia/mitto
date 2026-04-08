---
name: "Implement spec"
description: "Create a detailed implementation plan from a specification"
group: "Development"
backgroundColor: "#FFECB3"
---

Read the spec file thoroughly. Explore the codebase for current architecture,
patterns, and reusable utilities. Do not speculate — read relevant files first.

Read the spec, design the architecture, and create a detailed implementation plan
broken into small iterative steps.

Only plan and implement what the spec requires. Keep solutions simple. No extra
features, abstractions, or defensive coding beyond what's specified.

## Phase 1: Understand the Spec

1. Core requirements
2. Success criteria
3. Constraints (tech stack, dependencies)
4. Scope boundaries

Ask clarifying questions if anything is unclear.

## Phase 2: Design Architecture

Consider: data models, APIs/interfaces, business logic, error handling, security, testing strategy, observability, versioning.

Identify dependencies between components and optimal build order.

Use the `think` tool for deep reasoning about the design.

## Phase 3: Implementation Plan

Break into small iterative steps:
1. Identify major components and order
2. Break each into smaller steps
3. Ensure steps are: small enough for safe testing, large enough for progress, properly ordered



| # | Description | Files/Components | Dependencies | Verification |
|---|-------------|------------------|--------------|--------------|
| 1 | ... | ... | None | ... |
| 2 | ... | ... | Step 1 | ... |



Each step should be independently testable. Include test writing in each step, not as a separate phase. Prefer working software at every step.

Use the `todo` tool to track progress.

## Phase 4: Begin Implementation

Present plan, wait for approval.

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Approve and start / Modify plan"
**Without**: Ask in conversation.

Once approved, per step: implement → write/update tests → verify → report → next.

If issues arise that affect the plan, stop and discuss before continuing.
