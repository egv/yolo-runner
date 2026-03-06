# ADR-001: YAML Workflows Compiled to a Deterministic State Machine

Status: Proposed
Date: 2026-03-05
Codename: BENDER

## Context
BENDER needs configurable workflows for validation, clarification, sync, and execution. These workflows must be:
- human-editable
- testable and deterministic
- resumable
- observable

## Decision
- Workflow definitions are stored as versioned YAML documents.
- Workflows are validated and compiled into an executable graph/state machine.
- The engine emits a structured event stream for transitions and node execution.

## Consequences
### Positive
- Workflows are easy to review and change without code edits.
- The engine can be tested with deterministic node stubs.
- One workflow engine can be shared by Task Manager and Mastermind/Runner.

### Negative
- Requires disciplined schema versioning.
- Complex logic must be expressed via reusable node types/sub-workflows.

## Alternatives Considered
- Hard-coded state machines in each daemon (rejected: duplication and drift).
- A heavyweight workflow orchestrator (rejected: complexity and ops burden for MVP).
