# Workflow Schema v0

Status: Proposed
Scope: First concrete schema draft for parser/validator work. Documentation only; no implementation yet.

## Goals
- Make the PRD concrete enough to implement against without locking in every future feature.
- Keep v0 intentionally small and deterministic.
- Cover the first Mastermind validation/scheduling workflow and the M2 contract fixture.

## Top-Level Document Shape

```yaml
api_version: bender.workflow/v0
kind: workflow
name: mastermind.m2.validate_and_schedule
description: Validate task definition, request clarification if needed, otherwise enqueue a job.
inputs:
  - name: task_id
    type: string
    required: true
  - name: title
    type: string
    required: true
  - name: description
    type: string
    required: true
  - name: acceptance_criteria
    type: string
    required: false
nodes: []
edges: []
outputs: {}
```

Required top-level fields:
- `api_version`: versioned schema identifier. First draft is `bender.workflow/v0`.
- `kind`: fixed to `workflow` for now.
- `name`: stable workflow name used in events and persisted run state.
- `inputs`: ordered input declarations.
- `nodes`: ordered node definitions with stable IDs.
- `edges`: explicit transitions between nodes.
- `outputs`: named values exported from the completed run.

Optional top-level fields:
- `description`: human-readable summary.
- `metadata`: reserved key/value map for ownership, tags, or rollout hints.

## Input Declarations

Each input entry has the shape:

```yaml
- name: acceptance_criteria
  type: string
  required: false
  default: ""
  description: Free-form DoD / acceptance criteria text.
```

Allowed scalar types for v0:
- `string`
- `int`
- `bool`
- `string_list`

Validation rules:
- Input names must be unique.
- `default` must match the declared type.
- Required inputs may not omit a value at runtime unless a default is present.

## Nodes

Each node has the shape:

```yaml
- id: validate_task
  type: validate.task_definition
  with:
    required_fields:
      - acceptance_criteria
  timeout: 5s
  retry:
    max_attempts: 1
  idempotency: pure
```

Required fields:
- `id`: stable, unique node ID.
- `type`: handler type resolved from the node registry.

Optional fields:
- `with`: handler-specific configuration.
- `timeout`: duration string such as `5s` or `2m`.
- `retry`: retry policy.
- `idempotency`: hint for resume safety.

Retry shape:

```yaml
retry:
  max_attempts: 3
  backoff: 1s
  multiplier: 2.0
  max_backoff: 30s
```

Allowed idempotency hints for v0:
- `pure`: no side effects; always safe to rerun.
- `idempotent`: side effects allowed, but rerun is safe.
- `non_idempotent`: completion must be checkpointed before resume can skip rerun.

Canonical node outcomes:
- `success`
- `retryable_error`
- `fatal_error`
- `skipped`
- `cancelled`

## Edges

Each edge has the shape:

```yaml
- from: validate_task
  to: block_task
  when:
    outcome: success
    expr: outputs.ready == false
```

Required fields:
- `from`: source node ID.
- `to`: destination node ID.

Optional `when` fields:
- `outcome`: source-node outcome that must match.
- `expr`: deterministic boolean expression over the source node outputs and workflow inputs.

Determinism rules for v0:
- Edges are evaluated in file order.
- The first matching edge wins.
- `expr` may only reference:
  - `inputs.<name>`
  - `outputs.<name>` from the source node
- `expr` supports only `==`, `!=`, `&&`, `||`, parentheses, string literals, boolean literals, and list membership via `in`.

## Outputs

Run outputs are exported by name:

```yaml
outputs:
  final_status:
    from: set_in_progress.status
  job_id:
    from: enqueue_job.job_id
```

Rules:
- Output names must be unique.
- `from` must reference a declared node ID and one of that node's documented output fields.
- If the referenced node did not execute on the chosen path, the output is omitted from the exported run outputs.

## Initial Built-In Node Types

These are the first concrete handler names the docs assume exist, without starting implementation yet:
- `validate.task_definition`
  - input via `with.required_fields`
  - outputs: `ready` (`bool`), `missing_fields` (`string_list`)
- `task.set_status`
  - input via `with.status`, optional `with.reason`
  - outputs: `status` (`string`), `reason` (`string`)
- `task.add_comment`
  - input via `with.template`
  - outputs: `comment_id` (`string`)
- `runner.submit_job`
  - input via `with.job_kind`, optional `with.tags`
  - outputs: `job_id` (`string`)
- `control.finish`
  - input via `with.result`
  - outputs: `result` (`string`)

## Validation Rules

The v0 validator should reject workflows that:
- omit a required top-level field
- reuse an input or node ID
- reference an unknown node in an edge or output
- define a node with no outgoing edge and no terminal semantics
- define an edge expression that uses unsupported operators or unknown names

The v0 validator should warn when workflows:
- declare `non_idempotent` nodes without a timeout
- have unreachable nodes
- use a node type that is experimental or repo-local

## Example Workflow

```yaml
api_version: bender.workflow/v0
kind: workflow
name: mastermind.m2.validate_and_schedule
description: Validate a task, request clarification if acceptance criteria are missing, otherwise enqueue execution.
inputs:
  - name: task_id
    type: string
    required: true
  - name: title
    type: string
    required: true
  - name: description
    type: string
    required: true
  - name: acceptance_criteria
    type: string
    required: false
    default: ""
nodes:
  - id: validate_task
    type: validate.task_definition
    with:
      required_fields:
        - acceptance_criteria
    timeout: 5s
    retry:
      max_attempts: 1
    idempotency: pure

  - id: block_task
    type: task.set_status
    with:
      status: blocked
      reason: needs_info
    timeout: 5s
    retry:
      max_attempts: 3
      backoff: 1s
    idempotency: idempotent

  - id: comment_for_clarification
    type: task.add_comment
    with:
      template: "Please add acceptance criteria / definition of done so execution can start."
    timeout: 5s
    retry:
      max_attempts: 3
      backoff: 1s
    idempotency: idempotent

  - id: finish_blocked
    type: control.finish
    with:
      result: blocked_needs_info
    idempotency: pure

  - id: enqueue_job
    type: runner.submit_job
    with:
      job_kind: coding
      tags:
        - default
    timeout: 10s
    retry:
      max_attempts: 3
      backoff: 2s
    idempotency: idempotent

  - id: set_in_progress
    type: task.set_status
    with:
      status: in_progress
    timeout: 5s
    retry:
      max_attempts: 3
      backoff: 1s
    idempotency: idempotent

  - id: finish_enqueued
    type: control.finish
    with:
      result: enqueued
    idempotency: pure

edges:
  - from: validate_task
    to: block_task
    when:
      outcome: success
      expr: outputs.ready == false

  - from: validate_task
    to: enqueue_job
    when:
      outcome: success
      expr: outputs.ready == true

  - from: block_task
    to: comment_for_clarification
    when:
      outcome: success

  - from: comment_for_clarification
    to: finish_blocked
    when:
      outcome: success

  - from: enqueue_job
    to: set_in_progress
    when:
      outcome: success

  - from: set_in_progress
    to: finish_enqueued
    when:
      outcome: success

outputs:
  clarification_block_reason:
    from: block_task.reason
  enqueued_job_id:
    from: enqueue_job.job_id
```

Notes on the example:
- It makes the M2 clarification path explicit.
- It keeps task status vocabulary stable: `blocked` is the status; `needs_info` is the reason.
- It stops at enqueue/in-progress because execution completion is handled by later runner/job events, not this first validation workflow.

## Deferred From v0

These stay out of the first concrete schema draft:
- sub-workflows / call-by-reference
- parallel fan-out / fan-in
- rich type schemas beyond a small scalar set
- arbitrary scripting inside conditions
- dynamic node creation
