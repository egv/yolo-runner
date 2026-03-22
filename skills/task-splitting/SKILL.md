---
name: task-splitting
description: Split ADRs, PRDs, and implementation plans into strict TDD micro-tasks with explicit execution order for autonomous coding agents
license: MIT
compatibility: opencode
metadata:
  audience: maintainers
  workflow: planning
  domain: task-decomposition
---

## What I do

I turn a broad implementation request, ADR, PRD, or epic into a set of autonomous-agent-ready epics and tasks.

I optimize for:
- strict TDD
- minimal drift
- explicit dependencies
- fast review
- one-shot task execution

## When to use me

Use me when:
- a request is too large or vague for one coding run
- you need to create epics and tasks from an ADR or PRD
- a run stalled because a task was too broad
- you want to refactor an existing task tree into smaller slices
- you need a dependency-ordered plan for coding agents

## Core rules

Every task must:
- have exactly one primary seam
- have exactly one strict red-green loop
- have a narrow stop condition
- avoid mixing implementation, docs, integration wiring, and e2e
- include explicit out-of-scope boundaries
- name the expected files or subsystem touched
- be small enough to finish in one focused run

Default assumptions:
- prefer 1-3 production files
- prefer 1-2 test files
- prefer one verification command
- prefer helper-first decomposition before wiring
- prefer happy-path slices before teardown, fallback, and e2e

## Hard split heuristics

Split the task again if any of these are true:
- it includes more than one major verb joined by "and"
- it spans startup plus readiness plus execution plus teardown
- it requires more than one new abstraction
- it requires multiple unrelated test types
- it touches more than one subsystem boundary
- the first failing test is unclear
- the done condition is ambiguous
- the model could widen scope without violating the wording

## Anti-patterns

Bad task shapes:
- "Implement process driver and health checks"
- "Handle session, message, SSE, and permissions"
- "Wire defaults, docs, and e2e"
- "Refactor runtime and add tests"

These must be split.

## Ideal task shape

An ideal task is one of:
- add one helper
- parse one event
- map one error
- wire one call site
- flip one default
- add one focused regression test
- expose one config field
- translate one completion/result shape

## Required task template

For each task, produce:

- Title
- Why
- In scope
- Out of scope
- Strict TDD steps
- Done when
- Expected files
- Depends on
- Unlocks

Use this exact structure:

### Task: <title>

Why:
- <one sentence>

In scope:
- <specific behavior>
- <specific seam>

Out of scope:
- <explicit exclusions>

Strict TDD:
1. Add or update one targeted failing test first
2. Run the targeted test and confirm it fails for the intended reason
3. Implement the minimum production change needed to make it pass
4. Re-run the targeted test
5. Run one narrow follow-up verification command

Done when:
- <specific test or command passes>
- <specific behavior is verified>

Expected files:
- <prod files>
- <test files>

Depends on:
- <task IDs or none>

Unlocks:
- <task IDs or none>

## Output format

When splitting work, return:

1. Epics
2. Tasks under each epic
3. Dependency chain
4. Strict-TDD acceptance criteria
5. Warnings for tasks still too broad

Prefer this summary structure:

## Epics
- <epic name>: <goal>

## Tasks
- <task id or draft label>: <title>
- ...

## Order
- <task A> -> <task B> -> <task C>

## Risk notes
- <remaining ambiguity or split recommendation>

## Behavior for existing task trees

If given an existing epic/task tree:
- identify tasks that are too broad
- close or mark them as superseded in the plan
- replace them with smaller slices
- preserve dependency order
- ensure only the next intended slice is exposed as ready

## Strictness

When unsure, split smaller.
If a task seems "probably okay", it is usually still too big for an autonomous coding run.
