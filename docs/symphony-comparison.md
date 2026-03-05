# Borrowing Ideas From openai/symphony

This note compares `openai/symphony` (per `README.md`, `SPEC.md`, and `.codex/skills/*`) with this
repo (`yolo-runner`) and captures specific patterns we can borrow.

Sources (Symphony):
- `https://github.com/openai/symphony/blob/main/README.md`
- `https://github.com/openai/symphony/blob/main/SPEC.md`
- `https://github.com/openai/symphony/tree/main/.codex/skills`

## What Symphony Is

Symphony is a long-running orchestration service that:
- polls an issue tracker (Linear-first in the spec)
- creates an isolated workspace per issue
- runs Codex in *app-server mode* inside that workspace
- provides enough observability to operate multiple concurrent runs

An explicit boundary in the spec:
- orchestrator: scheduler/runner + tracker reader
- ticket writes (comments/state/PR links): usually performed by the coding agent via tools

## What yolo-runner Already Has That Maps Well

- Multi-backend agent runners (Codex CLI, OpenCode/ACP) and a richer local operator experience
  (`yolo-tui`, `yolo-webui`).
- Distributed execution infrastructure (Redis/NATS bus, executor/mastermind roles).
- Per-task clones/workspaces under `.yolo-runner/clones/<task-id>`.
- Ticket backends (GitHub + tk), plus dependency graphs.

## Key Things To Borrow

### 1) Repo-owned WORKFLOW contract (policy + runtime config)

Symphony keeps runtime policy in a single repo-owned file (`WORKFLOW.md`):
- YAML front matter (typed config)
- Markdown body (prompt template)

Borrowable ideas:
- A single “workflow contract” file per repo or per run profile.
- Strict template rendering rules (unknown variables fail) so prompts do not silently degrade.
- Environment indirection via `$VARS` inside YAML values.

Suggested fit for yolo-runner:
- Add an optional `WORKFLOW.md` that can *override / complement* `.yolo-runner/config.yaml`.
- Use it to centralize:
  - prompt templates (implement/review/rewrite)
  - timeouts, concurrency
  - safety posture (sandbox/approval settings)
  - workspace hooks (see below)

### 2) Dynamic reload with “last-known-good” fallback

Symphony’s spec requires watching `WORKFLOW.md` and hot-reapplying config/prompt for future
dispatches. Invalid reloads must not crash the service; keep operating with the last known good
configuration.

Suggested fit for yolo-runner:
- Watch `WORKFLOW.md` (and potentially `.yolo-runner/config.yaml`) and apply changes to:
  - polling / scheduling cadence
  - concurrency limits
  - runner selection defaults
  - prompt templates for future tasks
- Keep last-good parsed config in memory and surface reload errors in TUI/webui.

### 3) Workspace lifecycle hooks + explicit safety invariants

Symphony defines hooks:
- `after_create`, `before_run`, `after_run`, `before_remove`

And safety invariants:
- run the agent only with `cwd == workspace_path`
- workspace path must remain inside workspace root
- workspace key is sanitized

Suggested fit for yolo-runner:
- Formalize clone/workspace lifecycle hooks around `.yolo-runner/clones/<task-id>`:
  - `after_create`: bootstrap (checkout, deps)
  - `before_run`: ensure deps present
  - `after_run`: collect artifacts (test logs, coverage)
  - `before_remove`: best-effort cleanup
- Make the workspace invariants explicit and enforce them in runner adapters.

### 4) Codex app-server vs one-shot CLI

Symphony uses `codex app-server` (JSON-RPC-like over stdio) instead of `codex exec`.

What we get by adopting an app-server protocol:
- structured streaming events (no scraping stderr/stdout)
- stable correlation identifiers (`thread_id`, `turn_id`, derived `session_id`)
- built-in places to implement approvals and sandbox policy
- token + rate-limit telemetry for monitoring

How it relates to this repo:
- We already run OpenCode via ACP (structured protocol) in `internal/opencode/acp_client.go`.
- Codex support currently leans on CLI output parsing in `internal/codex/runner_adapter.go`.

Suggested fit:
- Implement a Codex app-server runner adapter analogous to the ACP runner:
  - start `codex app-server` in the task workspace
  - drive it with a small client (initialize/thread/start/turn/start)
  - emit runner events in the same schema consumed by TUI/webui

### 5) “Skills” as first-class operational playbooks

Symphony ships `.codex/skills/*` (commit, pull, push, land, debug, linear) as reusable playbooks.

Suggested fit for yolo-runner:
- Create a repo-local `skills/` (or `.codex/skills/` equivalent) used by both Codex and OpenCode
  runs to standardize:
  - commit message quality and staging discipline
  - safe push + PR creation
  - landing/CI watch loops
  - debug workflows (“find session id, trace logs, classify failure”)

This reduces prompt duplication and makes runs more deterministic.

### 6) Stable observability API + correlation keys

Symphony’s spec suggests a minimal HTTP API:
- `GET /api/v1/state`
- `GET /api/v1/<issue_identifier>`

And prescribes correlation keys for logs:
- `issue_identifier` / `issue_id` / `session_id`

Suggested fit:
- Define a stable JSON API contract for `yolo-webui` so the UI and TUI can share one canonical
  state model.
- Ensure every “run attempt” has a stable `session_id` (already possible for ACP; add for Codex).

### 7) Explicit orchestrator state machine and reconciliation

Symphony spends a lot of SPEC surface area on:
- claimed/running/retry-queued state
- reconciliation (stop runs when ticket becomes ineligible)
- stall detection and exponential backoff

Suggested fit:
- Make the mastermind/executor contract match a documented state machine.
- Avoid tight retry loops that generate noisy status flapping.
- Treat rate limits and transient tracker errors as first-class retry causes.

### 8) Reduce tracker writes (rate-limit resilience)

Symphony’s boundary (“scheduler reads; agent writes”) is a practical way to reduce write
amplification.

Suggested fit:
- For GitHub tracker mode, batch or debounce issue updates and prefer fewer writes.
- Implement explicit backoff on secondary rate limits (and surface a clear operator message).

## Practical Next Steps (Incremental)

1) Add a `WORKFLOW.md` loader + validator (front matter + prompt body) and allow it to override
   `.yolo-runner/config.yaml`.
2) Add file watch + reload + last-known-good.
3) Introduce workspace hooks around `.yolo-runner/clones/<task-id>`.
4) Implement Codex app-server adapter (keep `codex exec` as a fallback).
5) Define and version an observability JSON contract for webui/tui.
6) Adopt “skills” playbooks for commit/push/land/debug; reuse across Codex/OpenCode backends.
