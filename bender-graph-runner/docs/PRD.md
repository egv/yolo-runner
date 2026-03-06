# PRD: BENDER Graph Runner

Status: Draft
Codename: BENDER
Repo: bender-graph-runner

## Problem
BENDER needs configurable workflows (validation, clarification, sync, execution) that are:
- Human-editable and reviewable.
- Deterministic and testable.
- Resumable after crashes.
- Observable via a stable event stream.

We want one engine shared by Task Manager and Mastermind/Runner, instead of duplicating state machines in each component.

## Goals (MVP)
- A Rust workflow engine that:
  - Parses versioned YAML workflow definitions.
  - Validates and compiles workflows into an executable graph/state machine.
  - Executes runs with retries, timeouts, cancellation, and typed outcomes.
  - Persists run state for resume.
  - Emits a structured event stream (also serializable as NDJSON).
- A minimal CLI (`bender-graph`) for validating and running workflows locally.
- Shared contracts (`bender-contracts`): proto definitions and stable event schema consumed by all BENDER daemons.
- A cross-repo integration fixture (M2) that prevents Task Manager and Mastermind from drifting.

## Non-Goals (MVP)
- A distributed workflow scheduler.
- A workflow editor UI.
- A remote execution system for nodes (start in-process; revisit later).

## Users
- Task Manager engineers: run sync skills and readiness workflows.
- Mastermind engineers: run validation and orchestration workflows.
- Runner engineers: run execution/verification workflows.

## Concepts
- Workflow: versioned graph of nodes and edges.
- Node: an executable step with typed IO and explicit error classification.
- Run: one execution of a workflow, with persisted state and emitted events.
- Determinism: the engine itself makes deterministic choices; non-determinism lives in explicit node handlers.

## Functional Requirements
### Workflow format
- YAML schema includes: `api_version`, `name`, `inputs`, `nodes`, `edges`, `outputs`.
- Initial concrete draft lives in `docs/specs/workflow-schema-v0.md`.
- Strict validation with actionable errors.
- Stable node IDs for resume/debug.

### Execution semantics
- Node outcomes: `success`, `retryable_error`, `fatal_error`, `skipped`, `cancelled`.
- Per-node:
  - retry policy (attempt limit, backoff)
  - timeout
  - idempotency hint (for resume safety)
- Edge transitions can be conditional on outcome and/or output values.
- Sub-workflows (call-by-reference) to keep workflows small.

### Persistence and resume
- Persist after each node completion (or equivalent checkpoint).
- Resume a run without re-running completed nodes.
- Persist enough metadata to debug and reproduce (workflow hash/version, inputs, node outputs, timestamps).

### Observability
- Stable event types for:
  - run started/finished
  - node started/finished
  - retries, timeouts, cancellations
  - emitted log/output events
- Correlation IDs: workflow run ID, node ID, and external correlation fields (task/job IDs).

### Extensibility
- Node type registry:
  - built-in node handlers for core functionality
  - ability to add new handlers without changing engine core

## Non-Functional Requirements
- Deterministic core execution.
- No implicit side effects: any shelling out or network access must be an explicit node type.
- Fast validation and compilation for typical workflows.

## Interfaces
- Rust crates (names illustrative):
  - `bender-workflow-model`
  - `bender-workflow-yaml`
  - `bender-workflow-engine`
  - `bender-contracts`
- Initial shared RPC/event contract skeleton lives in `docs/specs/contracts-v0.md`.
- CLI:
  - `bender-graph validate <workflow.yaml>`
  - `bender-graph run <workflow.yaml> --inputs <json> --state-dir <dir>`

## Milestones
- M0:
  - YAML schema v0 + validator
  - executor for sequential graphs
  - persistence + resume
  - initial `bender-contracts`
- M1:
  - minimal built-in node types and a node registry
  - sub-workflow support
  - golden tests for event streams
- M2:
  - publish and maintain the M2 integration fixture for Task Manager <-> Mastermind
  - contract test harness design and CI expectations

## Risks
- YAML schema creep: mitigate via strict versioning and small, composable node types.
- Resume correctness for side-effecting nodes: mitigate with explicit idempotency semantics.

## Open Questions
- Persistence backend: file-based first vs SQLite from day one.
- State machine library vs bespoke minimal executor.
