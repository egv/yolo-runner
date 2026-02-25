# Ticket Frontmatter Schema

This document defines the ticket frontmatter contract used by all tk-style ticket files.

## Ticket Frontmatter Schema

`deps` and runtime overrides are supported by this contract:

| Field | Type | Required | Semantics |
| --- | --- | --- | --- |
| `id` | string | yes | Unique task identifier |
| `status` | string | yes | Ticket lifecycle state |
| `title` | string | yes | Human-readable summary |
| `deps` | array of strings | yes (can be empty) | List of task IDs this ticket depends on |
| `model` | string | no | Runner model override for this task |
| `backend` | string | no | Runner backend override (`opencode`, `codex`, `claude`, `kimi`, `gemini`) |
| `skillset` | string | no | Skill profile override for this task |
| `tools` | array of strings | no | Optional tool names available to the runner |
| `timeout` | string | no | Runner timeout override as a Go duration, for example `15m` |
| `mode` | string | no | Runner mode for this task (`implement`, `review`) |

### `deps` rules

- `deps` **must** be a YAML array in frontmatter, for example `deps: [yr-abc, yr-def]`.
- Every entry in `deps` must be a task identifier string.
- Empty list (`deps: []`) means no prerequisite tasks.
- A task is runnable only when all IDs listed in `deps` are marked complete.
- `deps` can be unsorted in authoring; parsers should treat it as a set for gating.
- Cyclic dependency references are invalid and should be rejected as invalid ticket graph input.

### Runtime config rules

- `model` must be a non-empty string.
- `backend` must be one of `opencode`, `codex`, `claude`, `kimi`, `gemini`.
- `skillset` must be a non-empty string.
- `tools`, when present, must be an array of non-empty strings.
- `timeout`, when present, must be a parseable Go duration and not less than `0`.
- `mode` must be `implement` or `review`.

### Per-task config example

```yaml
id: yr-task-a
status: open
title: Generate API docs
deps: [yr-base]
model: openai/gpt-5.3-codex
backend: codex
skillset: documentation
tools:
  - shell
  - git
timeout: 20m
mode: implement
```

## Dependency Example Patterns

### Serial dependencies
In a serial chain, each task depends on exactly one previous task.

```yaml
id: yr-serial-1
status: open
title: Prepare environment
deps: []

id: yr-serial-2
status: open
title: Run lints
deps: [yr-serial-1]

id: yr-serial-3
status: open
title: Run tests
deps: [yr-serial-2]
```

Execution order: `yr-serial-1 -> yr-serial-2 -> yr-serial-3`.

### Parallel dependencies
In a parallel pattern, one task can depend on multiple completed prerequisites.

```yaml
id: yr-parallel-1
status: open
title: Build binary
deps: []

id: yr-parallel-2
status: open
title: Run integration tests
deps: [yr-parallel-1]

id: yr-parallel-3
status: open
title: Run security checks
deps: [yr-parallel-1]

id: yr-parallel-4
status: open
title: Collect results
deps: [yr-parallel-2, yr-parallel-3]
```

Here `yr-parallel-2` and `yr-parallel-3` may execute in parallel, but `yr-parallel-4` waits for both.

## Core Test Cases for Dependency Graphs

Dependency graph test cases should cover:

- Linear chain: `A -> B -> C`
- Fan-out: `A -> B`, `A -> C`
- Fan-in: `A -> C`, `B -> C`
- Cycles: `A -> B -> C -> A` is invalid
