# Shared Contracts Skeleton v0

Status: Proposed
Scope: First concrete RPC and event skeleton for `bender-contracts`. Documentation only; no proto files or generated code yet.

## Goals
- Give Task Manager, Mastermind, Runner, and Graph Runner one shared vocabulary.
- Lock the M2 contract scenario to a stable status model before code starts.
- Define an event envelope that can be serialized to NDJSON without losing typed structure.

## Status Vocabulary

Task statuses for the first shared contract:
- `open`
- `blocked`
- `in_progress`
- `closed`

Block reasons for the first shared contract:
- `needs_info`
- `waiting_on_dependency`
- `external_hold`

Important rule:
- `needs_info` is a block reason attached to status `blocked`.
- `needs_info` is not a top-level task status.

## Proposed Package Layout

- `bender.contracts.task.v1`
- `bender.contracts.runner.v1`
- `bender.contracts.events.v1`
- `bender.contracts.common.v1`

This keeps the repo as the single source of truth while allowing downstream repos to depend on a small, versioned surface area.

## Core Messages (Proto-Style Sketch)

```proto
enum TaskStatus {
  TASK_STATUS_UNSPECIFIED = 0;
  TASK_STATUS_OPEN = 1;
  TASK_STATUS_BLOCKED = 2;
  TASK_STATUS_IN_PROGRESS = 3;
  TASK_STATUS_CLOSED = 4;
}

enum BlockReason {
  BLOCK_REASON_UNSPECIFIED = 0;
  BLOCK_REASON_NEEDS_INFO = 1;
  BLOCK_REASON_WAITING_ON_DEPENDENCY = 2;
  BLOCK_REASON_EXTERNAL_HOLD = 3;
}

enum JobStatus {
  JOB_STATUS_UNSPECIFIED = 0;
  JOB_STATUS_QUEUED = 1;
  JOB_STATUS_RUNNING = 2;
  JOB_STATUS_SUCCEEDED = 3;
  JOB_STATUS_FAILED = 4;
  JOB_STATUS_CANCELLED = 5;
}

enum NodeOutcome {
  NODE_OUTCOME_UNSPECIFIED = 0;
  NODE_OUTCOME_SUCCESS = 1;
  NODE_OUTCOME_RETRYABLE_ERROR = 2;
  NODE_OUTCOME_FATAL_ERROR = 3;
  NODE_OUTCOME_SKIPPED = 4;
  NODE_OUTCOME_CANCELLED = 5;
}

message TaskRef {
  string task_id = 1;
  string scope_id = 2;
}

message Task {
  TaskRef ref = 1;
  string title = 2;
  string description = 3;
  string acceptance_criteria = 4;
  TaskStatus status = 5;
  BlockReason block_reason = 6;
  int32 priority = 7;
  repeated string blocking_task_ids = 8;
}

message TaskComment {
  TaskRef ref = 1;
  string comment_id = 2;
  string body = 3;
  string author = 4;
}

message JobSpec {
  string job_id = 1;
  TaskRef task = 2;
  string job_kind = 3;
  repeated string tags = 4;
  map<string, string> labels = 5;
}

message JobResult {
  string job_id = 1;
  JobStatus status = 2;
  string summary = 3;
}
```

State rule:
- `Task.block_reason` should be set only when `Task.status == TASK_STATUS_BLOCKED`.
- Non-blocked tasks should carry `BLOCK_REASON_UNSPECIFIED`.

## RPC Surface for M2

Task Manager service skeleton:

```proto
service TaskManagerService {
  rpc ListReadyTasks(ListReadyTasksRequest) returns (ListReadyTasksResponse);
  rpc GetTask(GetTaskRequest) returns (GetTaskResponse);
  rpc UpdateTaskState(UpdateTaskStateRequest) returns (UpdateTaskStateResponse);
  rpc AddTaskComment(AddTaskCommentRequest) returns (AddTaskCommentResponse);
}
```

Runner service skeleton:

```proto
service RunnerService {
  rpc SubmitJob(SubmitJobRequest) returns (SubmitJobResponse);
  rpc GetJob(GetJobRequest) returns (GetJobResponse);
  rpc CancelJob(CancelJobRequest) returns (CancelJobResponse);
}
```

Minimal request/response expectations:
- `ListReadyTasks` returns ordered ready tasks for a scope.
- `GetTask` returns the full task definition used by validation workflows.
- `UpdateTaskState` sets status and optional `block_reason` atomically.
- `AddTaskComment` persists a clarification or execution comment.
- `SubmitJob` returns a stable `job_id`.

## Event Envelope

The event stream should use one stable envelope across daemons.

```proto
message EventEnvelope {
  string event_id = 1;
  uint64 sequence = 2;
  string source = 3;
  string workflow_run_id = 4;
  string workflow_name = 5;
  string node_id = 6;
  string task_id = 7;
  string job_id = 8;
  map<string, string> correlation = 9;
  string occurred_at = 10;

  oneof payload {
    RunStarted run_started = 20;
    RunFinished run_finished = 21;
    NodeStarted node_started = 22;
    NodeFinished node_finished = 23;
    TaskStateChanged task_state_changed = 24;
    TaskCommentAdded task_comment_added = 25;
    JobSubmitted job_submitted = 26;
    JobStarted job_started = 27;
    JobFinished job_finished = 28;
  }
}
```

Payload expectations:
- `RunStarted`: workflow inputs hash, workflow version, root correlation IDs.
- `RunFinished`: final run status and exported outputs.
- `NodeStarted`: attempt number and node type.
- `NodeFinished`: `NodeOutcome`, duration, and serialized outputs.
- `TaskStateChanged`: previous status, new status, and optional block reason.
- `TaskCommentAdded`: stable comment ID plus rendered body.
- `JobSubmitted`: emitted when Mastermind hands work to Runner.
- `JobStarted` / `JobFinished`: emitted by Runner with job execution state.

## NDJSON Serialization Rules

- One `EventEnvelope` per line.
- Field names should stay stable across proto and JSON representations.
- `occurred_at` should be RFC 3339 UTC.
- `sequence` must be monotonic within a single event stream.
- Unknown payload variants must be ignored by readers that do not understand them yet.

Example NDJSON lines:

```json
{"event_id":"evt-1","sequence":1,"source":"mastermindd","workflow_run_id":"run-102","workflow_name":"mastermind.m2.validate_and_schedule","task_id":"T-102","occurred_at":"2026-03-05T12:00:00Z","run_started":{"inputs_hash":"sha256:abc"}}
{"event_id":"evt-2","sequence":2,"source":"mastermindd","workflow_run_id":"run-102","workflow_name":"mastermind.m2.validate_and_schedule","node_id":"block_task","task_id":"T-102","occurred_at":"2026-03-05T12:00:01Z","task_state_changed":{"previous_status":"open","new_status":"blocked","block_reason":"needs_info"}}
{"event_id":"evt-3","sequence":3,"source":"mastermindd","workflow_run_id":"run-102","workflow_name":"mastermind.m2.validate_and_schedule","node_id":"comment_for_clarification","task_id":"T-102","occurred_at":"2026-03-05T12:00:02Z","task_comment_added":{"comment_id":"c-1","body":"Please add acceptance criteria / definition of done so execution can start."}}
```

## M2 Contract Implications

The M2 fixture can now rely on the following contract-level assertions:
- ambiguous tasks transition to `blocked` with `block_reason = needs_info`
- dependent tasks may use `blocked` with `block_reason = waiting_on_dependency`
- valid tasks move to `in_progress` when a job starts and `closed` when it finishes successfully

## Deferred From v0

These stay out of the first shared contract skeleton:
- streaming token/log payloads for agent backends
- authn/authz and multi-tenant policy fields
- backwards-compatibility guarantees for every future enum value
- full fixture-loading or admin/debug RPCs
