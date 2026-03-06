# ADR-003: M2 Integration Contract Test Is the First Cross-Repo Gate

Status: Proposed
Date: 2026-03-05
Codename: BENDER

## Context
Task Manager and Mastermind/Runner are developed in parallel in separate repos. This creates a high risk of:
- RPC drift (method names, shapes, semantics)
- readiness ordering drift
- status/comment writeback drift

We need an authoritative, repeatable end-to-end scenario that fails fast when the integration contract is broken.

## Decision
- Define a single M2 integration fixture and expected behaviors.
- Treat that fixture as an integration contract test.
- The fixture and its expectations live in `bender-graph-runner/contract-tests/m2/`.

The M2 contract test is the gate for:
- changes to `bender-contracts` (RPC/event schema)
- changes to Task Manager readiness or writeback semantics
- changes to Mastermind scheduling/validation semantics at the contract boundary

## Contract Scenario (M2)
Fixture includes at least:
- One valid task that should be executed and closed.
- One task blocked by the first task (dependency edge).
- One ambiguous task missing acceptance criteria that should be blocked and receive a clarification comment.

Expected system-level behaviors:
1. Mastermind requests ready tasks from Task Manager and receives an ordered set.
2. Mastermind fetches task details and runs validation workflow.
3. For ambiguous tasks, Mastermind writes back:
   - status set to blocked (reason: needs_info)
   - a comment requesting missing DoD/AC
4. For valid tasks, Mastermind schedules a JobSpec to Runner, and writes back:
   - status in_progress when execution starts
   - status closed when execution completes successfully
5. When the first task is closed, the dependent task becomes ready and is executed next.

Contract vocabulary note:
- `blocked` is the task status.
- `needs_info` is a block reason, not a top-level task status.

## Test Harness Expectations
- No external network required.
- All processes run locally with ephemeral ports.
- Deterministic outputs (timestamps may vary, but ordering and state transitions must match expectations).
- Event stream is captured as NDJSON for debugging.

## Consequences
### Positive
- Prevents silent drift between Task Manager and Mastermind.
- Forces early agreement on RPC and semantic boundaries.
- Provides a ready-made demo for M2.

### Negative
- Adds a cross-repo CI dependency (building and running multiple daemons).
- Requires maintenance as the contract evolves.

## Alternatives Considered
- Only unit tests per repo (rejected: does not catch semantic drift).
- Manual integration testing (rejected: not repeatable and too slow).
