# ADR-002: Server-Backed Agent Runtimes

## Status
Accepted

## Context

The current agent runner stack uses mixed execution models:

1. Codex is driven through direct CLI execution.
2. OpenCode is primarily driven through ACP.
3. Other backends use backend-specific CLI adapters.

This makes lifecycle control inconsistent across backends. Process startup, readiness, streaming, approvals, cancellation, teardown, and logging all behave differently depending on the adapter.

We want tighter control of task execution by starting a backend-specific server process for each task, connecting to it programmatically, and killing it when the task finishes. This should become the default mode for supported backends.

The relevant protocol surfaces are different:

- `codex app-server` uses JSON-RPC 2.0 over `stdio` by default.
- `opencode serve` exposes HTTP APIs plus SSE event streams.
- ACP is a separate structured protocol already used by the current OpenCode runner.

These protocols are similar at the task lifecycle level, but not similar enough at the raw wire level to justify one thin transport client.

We also explicitly do not want interactive or user-managed sessions in yolo-runner. Any backend thread or session must be internal, ephemeral, and owned entirely by the runner.

## Decision

We will introduce a shared task-session runtime abstraction above backend-specific protocols.

### 1. Shared runtime layer

The shared runtime is responsible for:

- starting a child process for a task
- waiting for readiness
- executing the task request
- normalizing backend events into runner progress events
- handling approvals and questions
- handling cancellation and timeouts
- collecting logs and artifacts
- shutting the child process down cleanly, then force-killing if needed

### 2. Codex default backend

The default `codex` backend will use `codex app-server` over `stdio` JSON-RPC.

- One app-server process is started per task.
- The runner initializes the connection, starts one internal thread, starts one turn, consumes notifications until completion, and then tears everything down.
- The thread/turn model is treated as backend plumbing only, not as a user-visible session feature.

### 3. OpenCode default backend

The default `opencode` backend will use `opencode serve` over loopback HTTP/SSE.

- One server process is started per task.
- The runner waits for health readiness, creates one internal task session, sends one task request, consumes SSE events until completion, and then tears everything down.
- The session model is treated as backend plumbing only, not as a user-visible session feature.

### 4. ACP reuse

ACP will be migrated onto the same shared runtime abstraction later so it can reuse:

- process lifecycle management
- normalized event mapping
- approval/question handling
- timeout and cancellation behavior
- logging and artifacts

ACP remains available as an explicit fallback during migration.

### 5. Legacy fallbacks

Legacy execution modes remain available explicitly during the migration:

- `codex-cli`
- `opencode-acp`
- optionally `opencode-cli` if still needed by existing flows

The built-in backend names `codex` and `opencode` will refer to the new server-backed defaults.

## Consequences

### Positive

- consistent lifecycle management across backends
- better control over readiness, cancellation, and teardown
- structured event handling instead of backend-specific ad hoc parsing
- shared approval and question handling
- cleaner cutover from CLI- and ACP-specific implementations
- a clear path to reuse the same runtime for ACP

### Negative

- more architectural complexity in the runner layer
- additional protocol adapters to maintain
- per-task process startup overhead
- more end-to-end test surface

### Risks

- protocol mapping bugs between backend events and normalized runner events
- drift in Codex or OpenCode server APIs
- review-mode parity differences during migration
- duplicated logic during the temporary fallback period

## Implementation Plan

1. Add the shared task-session runtime.
2. Implement the Codex app-server adapter on top of it.
3. Implement the OpenCode serve adapter on top of it.
4. Change default backend definitions to use server-backed modes.
5. Keep explicit legacy fallbacks.
6. Migrate ACP onto the same runtime abstraction.

## Alternatives Considered

### Option A: Keep backend-specific CLI and ACP runners

Rejected because lifecycle control remains fragmented and duplicated.

### Option B: Build one raw API abstraction for all protocols

Rejected because JSON-RPC, HTTP/SSE, and ACP differ too much at the wire level.

### Option C: Use long-lived shared backend servers

Rejected because per-task isolation and teardown control are more important than avoiding process startup cost.

### Option D: Use Codex websocket app-server mode by default

Rejected because `stdio` is simpler, more local, and easier to supervise for per-task execution.

## Related

- `internal/codex`
- `internal/opencode`
- `internal/codingagents`
- `cmd/yolo-agent`
- `cmd/yolo-linear-worker`
- `docs/plans/2026-01-25-acp-opencode-runner-design.md`

## Decision Date
2026-03-18

## Decision Makers
- egv

## Notes

This ADR intentionally chooses a shared lifecycle abstraction, not a shared wire protocol abstraction.
