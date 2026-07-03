# Yolo Runner

AI-powered task execution system with pluggable storage backends (GitHub, Linear, TK), dependency-aware scheduling, and smart concurrency calculation. The runner owns task selection, status updates, and logging; agents execute tasks they're given.

## Features

- **Pluggable Storage Backends**: GitHub Issues, Linear, or local TK (markdown) tickets
- **Task Engine**: Graph-based scheduler with dependency resolution and parent-child hierarchies
- **Smart Concurrency**: Automatically calculates optimal parallel execution from dependency graphs
- **TDD Mode**: Strict Red/Green/Refactor enforcement for test-driven development
- **Structured Logging**: JSONL event streams with log browser TUI
- **Installation Scripts**: One-line install via `install.sh` or `install.ps1`
- **Multi-Backend Support**: OpenCode, Codex, Claude, Kimi

## CLI Tools

- `yolo-agent` - Task orchestration and scheduling
- `yolo-tui` - Real-time event monitoring with log browser

See `MIGRATION.md` for historical command mapping.

## Installation

### One-Line Install

```bash
# macOS/Linux
curl -sSL https://raw.githubusercontent.com/egv/yolo-runner/main/install.sh | bash

# Windows PowerShell
irm https://raw.githubusercontent.com/egv/yolo-runner/main/install.ps1 | iex
```

### From Source

```bash
make install
```

### Verify Installation

```bash
./bin/yolo-agent --version
./bin/yolo-tui --version
```

## Storage Backends

Yolo-runner supports multiple task storage backends:

### GitHub Issues

```yaml
# .yolo-runner/config.yaml
profiles:
  github:
    tracker:
      type: github
      github:
        scope:
          owner: egv
          repo: yolo-runner
        auth:
          token_env: GITHUB_TOKEN
```

### Linear

```yaml
profiles:
  linear:
    tracker:
      type: linear
      linear:
        scope:
          workspace: my-workspace
        auth:
          token_env: LINEAR_API_KEY
```

### TK (Local Markdown)

```yaml
profiles:
  tk:
    tracker:
      type: tk
```

TK stores tickets as markdown files in `.tickets/` with frontmatter for metadata.

## GUI Architecture Requirements

The production stdin monitor (`yolo-tui`) follows an Elm-style `Model/Update/View` architecture and uses:

- Bubble Tea for event-driven terminal application state updates
- Bubbles for reusable UI components and interaction primitives
- Lip Gloss for deterministic styling/layout output

These UI dependencies are mandatory for GUI workflow evolution and should be treated as part of the runtime contract.

## Current Orchestration Model

- `yolo-agent` owns task selection, dependency-aware scheduling, retries, review, and event emission.
- `yolo-tui` consumes the event stream for monitoring.

### Queue-split runner topology

Queue-split runs use separate source and runner processes around one local SQLite queue. Source processes poll external systems, enqueue typed work, and write results back. Runner processes claim queued work, materialize a workspace from an environment preset, execute the model pipeline, and record the result.

For normal operations, prefer `yolo-agent watch`; it supervises the configured sources and autoscaled runner pools in one process. Use the separate commands below as fallback or focused debugging tools.

Operator commands:

- Source adapters: `yolo-agent source <arcpr|startrek> --profile <name> --queue ~/.yolo-runner/queue.db`
- Beads debug source: `yolo-agent source br --repo . --queue ~/.yolo-runner/queue.db --preset <preset> [--root <epic-id>] [--name <source-name>] --once --stream --events <path>`
- Runner daemons: `yolo-agent runner --queue ~/.yolo-runner/queue.db --environments ~/.yolo-runner/environments.yaml --presets <preset>[,<preset>]`
- Queue inspection: the queue is plain SQLite at `~/.yolo-runner/queue.db` (tables `work_items`, `work_results`, `item_deps`, `runners`); a `yolo-agent queue` operator CLI (`ls`/`submit`/`retry`/`cancel`/`gc`) is planned.
- Merged event stream: `yolo-agent events follow --since 1h | yolo-tui --events-stdin`
- Supervisor: `yolo-agent watch --repo . --environments ~/.yolo-runner/environments.yaml --tui`

Environment presets live in `~/.yolo-runner/environments.yaml`. Work items carry only the preset name; runners resolve workspace strategy, landing policy, agent backend/model, concurrency limits, and environment passthrough at claim time. See [docs/environment-presets.md](docs/environment-presets.md) for the full schema and a copy-ready [example](docs/environments.example.yaml).

Example:

```yaml
presets:
  yolo-runner:
    workspace:
      strategy: git-clone
      origin: ~/yolo-runner
      base_branch: main
    landing:
      type: git-merge
    agent:
      backend: codex
      model: openai/gpt-5.3-codex
    limits:
      max_concurrent: 2
  adapta:
    workspace:
      strategy: arc-shared
      mount: ~/arcadia
      subpath: marvel/gena/adapta
    landing:
      type: arc-pr
    agent:
      backend: codex
      model: gpt-5.5
    limits:
      max_concurrent: 1
```

The runner isolates by work kind, not by a preset flag. Code-writing kinds (`implement`, `finalize`) always run in a fresh isolated, VCS-bearing workspace — a per-item git clone fast-forwarded to `base_branch`, or a per-item branch on the arc mount — and a code-writing item is rejected if it would have no VCS, so it can never mutate the source checkout. Read-only kinds (`preflight`, `split`, `pr-review`) get a lightweight parallel-safe read view (for arc, the mount with no per-item branch and no lock), so reviews and triage run concurrently. The single-command `yolo-agent --queue` path synthesizes a git-clone preset from the repo automatically; arc repos require an explicit `arc-shared` preset and a standalone `runner`.

Each process writes JSONL to `~/.yolo-runner/events/<proc-id>.jsonl`. Use `yolo-agent events follow` to merge-tail those files by timestamp into the unchanged TUI stdin protocol. `tracker-watch` and `arc-review-watch` are deprecated compatibility shims; prefer `yolo-agent source startrek` and `yolo-agent source arcpr` for queue-backed runs.

### Watch configuration

`yolo-agent watch` starts configured sources and autoscaled runner pools from one command. Use `--tui` to open the existing live monitor directly; omit it for headless operation. `watch.tui.default_mode: ui` makes the TUI the config default, while `stream` keeps NDJSON on stdout for pipes and service logs.

`.yolo-runner/config.yaml`:

```yaml
default_profile: startrek-adapta
profiles:
  startrek-adapta:
    tracker:
      type: startrek
      startrek:
        endpoint: https://st-api.example.test
        token_env: STARTREK_TOKEN
        queues:
          - key: VAY
            assignee: genaevstratov   # required: pick up issues assigned to this Startrek user
            label: yolo-agent-ready   # optional, defaults to yolo-agent-ready
            preset: adapta
            root: ~/arcadia/marvel/gena/adapta
  arc-review:
    tracker:
      type: tk

tracker_agent:
  poll_interval: 30s

arc_review_watch:
  poll_interval: 30s
  reviewer: alice
  allow_ship: false
  objects_base_dir: ~/.yolo-runner/pr-objects
  mounts_base_dir: ~/.yolo-runner/pr-mounts

watch:
  queue_path: .yolo-runner/watch.db
  sources:
    - name: startrek-adapta
      type: startrek
      profile: startrek-adapta
    - name: arc-review
      type: arcpr
      profile: arc-review
  runner_pools:
    - name: adapta-implementers
      source: startrek-adapta
      presets: [adapta]
      min_replicas: 1
      max_replicas: 4
      capacity: 1
    - name: arc-reviewers
      source: arc-review
      presets: [arc-review]
      min_replicas: 1
      max_replicas: 3
      capacity: 2
  autoscale:
    min_runners: 1
    max_runners: 7
  tui:
    default_mode: stream
```

`~/.yolo-runner/environments.yaml`:

```yaml
presets:
  adapta:
    workspace:
      strategy: arc-shared
      mount: ~/arcadia
      subpath: marvel/gena/adapta
    landing:
      type: arc-pr
      title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"
    agent:
      backend: codex
      model: gpt-5.5
      runner_timeout: 20m
      watchdog_timeout: 10m
      watchdog_interval: 5s
    limits:
      max_concurrent: 1
    env:
      passthrough: [STARTREK_TOKEN, ARC_TOKEN]

  arc-review:
    workspace:
      strategy: path
      path: ~/arcadia
    landing:
      type: none
    agent:
      backend: codex
      model: gpt-5.5
      runner_timeout: 20m
      watchdog_timeout: 10m
      watchdog_interval: 5s
    limits:
      max_concurrent: 2
    env:
      passthrough: [ARC_TOKEN]
```

Validate and run:

```bash
export STARTREK_TOKEN=<startrek-api-token>
export ARC_TOKEN=<arc-token>
./bin/yolo-agent config validate --repo .
./bin/yolo-agent watch --repo . --environments ~/.yolo-runner/environments.yaml --tui
```

See [docs/watch-supervisor.md](docs/watch-supervisor.md) for the full watch supervisor playbook, multi-queue Startrek preset routing, autoscaler tuning, recovery steps, and manual split-process fallback commands.

## What It Does

- Loads tasks from tracker/storage backends such as GitHub, Linear, TK, or beads/br.
- Builds a dependency graph and calculates runnable concurrency.
- Runs the selected coding-agent backend for implementation and review.
- Writes structured JSONL events and per-task backend logs.
- Updates task status/data and manages task clones under `.yolo-runner/clones/`.

## Requirements

- `opencode` CLI available.
- `git` installed and repo cloned.
- Go 1.21+ for building the runner.
- `gopls` available on `PATH` (required by Serena/OpenCode for Go language services).

## Build

From repo root:

```
make build
```

## Version Management

### Check Version

```bash
./bin/yolo-agent --version
./bin/yolo-tui --version
```

## Installation Matrix

Supported platforms:

| Platform | Architecture | Install Method |
|----------|--------------|----------------|
| macOS    | amd64, arm64 | install.sh, make install, release |
| Linux    | amd64, arm64 | install.sh, make install, release |
| Windows  | amd64        | install.ps1, release |

Installation verification: `docs/install-matrix.md`

## Test

```
make test
```

## Release Gates

### E8 Self-Hosting Demos

Run the E8 release gate after self-hosting demos:

```bash
make release-gate-e8
```

Verifies:
- `TestE2E_CodexTKConcurrency2LandsViaMergeQueue`
- `TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage`
- `TestE2E_KimiLinearProfileProcessesAndClosesIssue`
- `TestE2E_GitHubProfileProcessesAndClosesIssue`

### CI/CD Workflows

**GitHub Actions:**
- `.github/workflows/ci.yml` - Build and test on push/PR
- `.github/workflows/release.yml` - Automated releases on tags

**Release Process:**
1. Tag: `git tag v1.2.3`
2. Push: `git push origin v1.2.3`
3. Release workflow publishes artifacts
4. Install script pulls latest

### E8 Release Gate Checklist

After completing the E8 self-host demos, run the release gate checklist:

```
make release-gate-e8
```

The gate verifies these acceptance tests:

- `TestE2E_CodexTKConcurrency2LandsViaMergeQueue`
- `TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage`
- `TestE2E_KimiLinearProfileProcessesAndClosesIssue`
- `TestE2E_GitHubProfileProcessesAndClosesIssue`

It also validates docs contracts for this checklist and the migration guidance.

## Repo-local OpenCode Assets

Copy these bundled assets into the repository-local `.opencode/` tree before running OpenCode-backed flows:

- `yolo.md` -> `.opencode/agent/yolo.md`
- `agent/release.md` -> `.opencode/agent/release.md` (when present)
- `skills/task-splitting/SKILL.md` -> `.opencode/skills/task-splitting/SKILL.md`
- `commands/split-tasks.md` -> `.opencode/commands/split-tasks.md`
- `commands/split-tasks-strict.md` -> `.opencode/commands/split-tasks-strict.md`

These files are intentionally repo-local so task clones inherit the same OpenCode agent, skill, and command behavior.

### Task Splitting Skill

The repo ships a reusable OpenCode task-splitting skill plus two command wrappers:

- `/split-tasks`
- `/split-tasks-strict`

Use them to turn ADRs, PRDs, or broad implementation requests into strict-TDD epics and micro-tasks with explicit dependency order.

Examples:

```text
/split-tasks @docs/adr/ADR-002-server-backed-agent-runtimes.md
```

```text
/split-tasks-strict Break this feature request into the smallest useful tasks for an autonomous coding agent.
```

## Features & Flags

### Task Engine (Graph-Based Scheduling)

The Task Engine builds a directed graph from task relationships:

- **Dependencies**: `depends-on` relationships block tasks until dependencies complete
- **Parent-Child**: Epic/task hierarchies are respected
- **Smart Concurrency**: Automatically calculated from graph structure

Example dependency in ticket frontmatter:
```yaml
---
id: task-123
deps: [task-456, task-789]
---
```

### Concurrency Calculation

Concurrency is calculated dynamically based on the dependency graph:

```bash
# Auto-calculate from graph (respects dependencies)
./bin/yolo-agent --repo . --root <epic> --concurrency auto

# Fixed concurrency (default: 1)
./bin/yolo-agent --repo . --root <epic> --concurrency 3
```

### TDD Mode (Strict Test-Driven Development)

Enforces Red/Green/Refactor workflow:

```bash
./bin/yolo-agent --repo . --root <epic> --tdd
```

When `--tdd` is enabled:
- Tests must be written first (RED)
- Implementation makes tests pass (GREEN)
- Refactor while keeping tests green

### Task Quality Gate

Validates task clarity before execution:

```bash
./bin/yolo-agent --repo . --root <epic> --quality-gate
```

Checks for:
- Clear description
- Concrete acceptance criteria
- No vague language ("maybe", "consider")
- Required fields present

### Log Browser TUI

Browse logs grouped by task:

```bash
./bin/yolo-tui --events-stdin < runner-logs/run.events.jsonl
```

Features:
- Tree view of tasks and epics
- Search/filter logs
- View agent thoughts and decisions
- Export logs

## Run

From repo root:

```
./bin/yolo-agent --repo . --root algi-8bt --model gpt-4o
./bin/yolo-agent --repo . --root algi-8bt --dry-run
```

Pipe `--stream` output into `yolo-tui` for live monitoring.

Common options:
- `--max N` limit number of tasks processed
- `--dry-run` print the task prompt without running OpenCode
- `--concurrency N` or `--concurrency auto` - Parallel task execution (default: 1)
- `--tdd` enable strict TDD mode (Red/Green/Refactor)
- `--quality-gate` validate task clarity before execution
- `--mode stream|ui` set output mode for event delivery
- `--stream` output JSONL events for TUI consumption
- `--events PATH` write events to file
- `--retry-budget N` max retries per task (default: 5)
- `--profile NAME` use tracker profile from config
- `--backend codex|opencode|claude|kimi|gemini` agent backend
- `--model MODEL` model name (e.g., openai/gpt-5.3-codex)
- `--runner-timeout DURATION` per-task timeout (e.g., 20m)

### Streaming Mode (Real-time TUI)

Stream events to TUI for real-time monitoring:

```bash
./bin/yolo-agent --repo . --root <root-id> --stream | ./bin/yolo-tui --events-stdin
```

Save events to file while streaming:

```bash
./bin/yolo-agent --repo . --root <root-id> --stream --events "run-$(date +%Y%m%d).events.jsonl" | ./bin/yolo-tui --events-stdin
```

TDD mode with streaming:

```bash
./bin/yolo-agent --repo . --root <root-id> --tdd --stream | ./bin/yolo-tui --events-stdin
```

The TUI is decoder-safe: malformed JSONL lines are surfaced as warnings while valid events continue rendering.

### `yolo-agent` preflight (commit + push first)

Always commit and push ticket/config changes before starting `yolo-agent`.

- Required before run: commit `.tickets/*.md` and related config/code changes, then run `git push`.
- Why: each task runs in a fresh clone that syncs against `origin/main`; local-only commits are not visible in task clones.
- Symptom when skipped: runner output shows errors like `ticket '<id>' not found` in clone context.

Quick preflight:

```
git status --short
git push
export GITHUB_TOKEN=$(gh auth token)
./bin/yolo-agent --repo . --root <root-id> --backend codex --concurrency 3 --events "runner-logs/<run>.events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

If a run is interrupted, reset state before restarting:

1. Stop `yolo-agent`.
2. Move interrupted tasks back to `open`.
3. Remove stale clone directories under `.yolo-runner/clones/<task-id>`.
4. Do not edit `scheduler-state.json`; it is no longer written. Queue-backed runs recover from queue leases, and source adapters reconcile from tracker/source truth on restart.

### Tracker agent PoC runbook

Operator steps for the Startrek tracker-agent PoC, including watch supervisor startup, per-queue preset routing, the legacy beads-profile epic command, Arc PR landing config, labels, dry-run checks, and reset procedure, are documented in `docs/tracker-agent-poc.md`.

### Arc review watch operator runbook

Operator steps for Arc PR review via `yolo-agent watch`, the `source arcpr` fallback, legacy `yolo-agent arc-review-watch`, event/TUI usage, SQLite reset, stale process recovery, ship instructions, and first-run `allow_ship: false` guidance are documented in `docs/arc-review-watch.md`.

### `--runner-timeout` profiles (`yolo-agent`)

Use `--runner-timeout` to cap each task execution. Start with these defaults and tune for your repo/task size.

- Default behavior (flag omitted): `--runner-timeout 0s` (no hard per-runner deadline) plus the no-output watchdog (10m default) still prevents indefinite hangs.
- Local profile: `--runner-timeout 10m` keeps hangs bounded while still allowing normal coding loops.
- CI profile: `--runner-timeout 20m` allows slower shared runners and heavier validation steps.
- Long-task profile: `--runner-timeout 45m` for large refactors or slower model/provider backends.

Examples:

```
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --runner-timeout 10m
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --runner-timeout 20m
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --runner-timeout 45m
```

### `yolo-agent` config defaults (`.yolo-runner/config.yaml`)

`yolo-agent` can load defaults from the `agent:` block in `.yolo-runner/config.yaml`.

Example:

```yaml
default_profile: default
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 2
  runner_timeout: 20m
  watchdog_timeout: 10m
  watchdog_interval: 5s
  retry_budget: 5
```

Precedence rules:

- Backend: `--agent-backend > --backend > YOLO_AGENT_BACKEND > agent.backend > codex`
- Profile: `--profile > YOLO_PROFILE > default_profile > default`
- Model and numeric/duration defaults: CLI flag value wins; if unset, `agent.*` value is used.
- Retry budget defaults to `5` per task when neither `--retry-budget` nor `agent.retry_budget` is set.

Validation rules for `agent.*` values:

- `agent.backend` must be one of `opencode`, `opencode-serve`, `opencode-acp`, `codex`, `codex-cli`, `claude`, `kimi`, `gemini`.
- `agent.mode` must be one of `stream`, `ui` when set; omit for headless (default: no streaming).
- `agent.concurrency` must be greater than `0`.
- `agent.runner_timeout` must be greater than or equal to `0`.
- `agent.watchdog_timeout` must be greater than `0`.
- `agent.watchdog_interval` must be greater than `0`.
- `agent.retry_budget` must be greater than or equal to `0`.

Invalid config values fail startup with field-specific errors that reference `.yolo-runner/config.yaml`.

### Gemini backend setup

To use the Gemini backend:

- Ensure the `gemini` CLI is on `PATH`.
- Set `GEMINI_API_KEY` in your environment.
- Point `agent.backend` to `gemini` in `.yolo-runner/config.yaml`, or pass `--backend gemini`.
- Select an allowed model like `gemini-2.5-flash` or `gemini-2.0-pro`.

Example:

```yaml
agent:
  backend: gemini
  model: gemini-2.5-flash
```

### `yolo-agent config` init/validate workflow

Use `config init` to scaffold a starter config, then run `config validate` before starting longer agent runs.

Bootstrap:

```bash
./bin/yolo-agent config init --repo .
```

If the file already exists and you intentionally want to overwrite it:

```bash
./bin/yolo-agent config init --repo . --force
```

Validate in human-readable mode:

```bash
./bin/yolo-agent config validate --repo .
```

Typical success output:

```text
config is valid
```

Typical failure output:

```text
config is invalid
field: agent.concurrency
reason: must be greater than 0
remediation: Set agent.concurrency to an integer greater than 0 in .yolo-runner/config.yaml.
```

Machine-readable validation (for CI hooks):

```bash
./bin/yolo-agent config validate --repo . --format json
```

Troubleshooting details and additional failure/remediation cases are documented in `docs/config-workflow.md`.

## Task Management

### Creating Tickets

**TK (Local Markdown):**
```bash
tk create "Task title" -t task -p 1
tk create "Epic title" -t epic -p 0
tk dep <task-id> <depends-on-id>  # Add dependency
tk link <task1> <task2>          # Link related tasks
```

**GitHub Issues:**
Standard GitHub issue creation with sub-issues for hierarchy.

### Ticket Frontmatter Schema

```yaml
---
id: unique-id
parent: parent-epic-id  # For hierarchy
deps: [dep1, dep2]       # Dependencies that block this task
status: open|in_progress|closed
type: task|epic|bug
priority: 0-4            # 0=highest, 4=lowest
assignee: username
---
```

Full schema: `docs/ticket-frontmatter-schema.md`

## Task Prompt

The prompt includes:
- Bead ID and title
- Description
- Acceptance criteria
- Strict TDD rules

The runner selects work by traversing container types (epic, molecule). Traversable containers are in `open` or `in_progress` status, and leaf work is eligible when it is open only.

The YOLO agent must only work on the prompt provided. It must not call beads commands.

## Structured Logging

All events are emitted as JSONL (newline-delimited JSON) with consistent schema:

```json
{"type": "task_started", "task_id": "abc-123", "task_title": "...", "ts": "2026-02-22T10:00:00Z"}
{"type": "agent_text", "task_id": "abc-123", "message": "...", "ts": "2026-02-22T10:00:05Z"}
{"type": "task_finished", "task_id": "abc-123", "metadata": {"status": "completed"}, "ts": "2026-02-22T10:05:00Z"}
```

Log locations:
- Events: `runner-logs/<run-id>.events.jsonl`
- Agent output: `.yolo-runner/clones/<task-id>/runner-logs/`
- Schema: `docs/logging-schema.md`

### Log Browser

Browse logs interactively:

```bash
# From saved events
./bin/yolo-tui --events-file runner-logs/run.events.jsonl

# From stdin
cat runner-logs/run.events.jsonl | ./bin/yolo-tui --events-stdin
```

Features:
- Tree view organized by epic → task
- Filter by event type
- Search messages
- View agent thoughts and tool calls
- Export filtered logs

## Task Logs

- Event stream: `runner-logs/*.events.jsonl`
- Per-task backend logs: `.yolo-runner/clones/<task-id>/runner-logs/`

## Troubleshooting: output looks stuck

- Tail the OpenCode log: `tail -f runner-logs/opencode/opencode.log`
- Identify the current task: run `bd show <issue-id>` from the last "selected bead" line in the output

If OpenCode/Serena fails during startup you may see errors like "gopls is not installed" and the run can end up idle.
Install `gopls` via Go and ensure it is on `PATH`:

```
GOBIN=~/.local/bin go install golang.org/x/tools/gopls@latest
```

## OpenCode Config Isolation

The runner sets `XDG_CONFIG_HOME=~/.config/opencode-runner` so OpenCode reads and writes config in an isolated directory instead of your default `~/.config/opencode`.

If flags are added later to change the config location, use those to override the default. Otherwise inspect the effective config by checking `~/.config/opencode-runner` directly or exporting a different `XDG_CONFIG_HOME` before running the binary.

## Manual Smoke Test

1. Create a throwaway branch and ensure the repo is clean.
2. Confirm the repo-local `.opencode/` assets are installed.
3. Run `./bin/yolo-agent --repo . --root <root-id> --max 1 --stream | ./bin/yolo-tui --events-stdin`.
4. Inspect the resulting commit and confirm it only includes the expected task changes.
5. Review the emitted event log and the per-task backend log under `.yolo-runner/clones/<task-id>/runner-logs/`.

Success looks like: the agent run finishes without errors, task status/data are updated as expected, and the logs show a complete implementation/review cycle.

## Session Completion

After finishing a batch of tasks:

```bash
# Close completed epics
tk epic close-eligible

# Or for GitHub
git issue list --state closed | gh issue edit <epic> --add-label "completed"

# Clean up stale clones
rm -rf .yolo-runner/clones/*
```

This keeps `tk ready` output clean and removes old working directories.

## Troubleshooting

### Agent Or Skill Not Found

- Confirm `.opencode/agent/yolo.md` exists.
- Confirm it includes `permission: allow`.
- Confirm `.opencode/skills/task-splitting/SKILL.md` exists when task splitting is expected.
- Confirm `.opencode/commands/split-tasks.md` and `.opencode/commands/split-tasks-strict.md` exist when those commands are expected.

### Task Not Found in Clone

**Cause:** Ticket/config changes not pushed to origin.

**Fix:**
```bash
git add .tickets/*.md .yolo-runner/config.yaml
git commit -m "Add ticket/config changes"
git push
```

### Stale Clone State

If a run is interrupted:

```bash
# Stop agent
pkill yolo-agent

# Reset task status
tk status <task-id> open

# Remove stale clone
rm -rf .yolo-runner/clones/<task-id>

# scheduler-state.json is obsolete; queue leases and source reconciliation
# recover interrupted work.
```

### Review Failures (TDD Mode)

When using `--tdd`, review may fail if:
- Production code is written before tests
- Tests don't fail first (RED phase)
- Implementation is too broad

**Fix:** Remove production code, keep only failing tests, retry.

### Debug Logging

Enable verbose output:

```bash
./bin/yolo-agent --repo . --root <epic> --stream --verbose 2>&1 | tee debug.log
```

### OpenCode Asset Issues

If startup fails with agent/skill/command errors:

1. Reinstall the repo-local `.opencode/` assets from the tracked source files in this repo.
2. Confirm `.opencode/agent/yolo.md` exists and includes `permission: allow`.
3. Confirm `.opencode/skills/task-splitting/SKILL.md` exists.
4. Confirm `.opencode/commands/split-tasks.md` and `.opencode/commands/split-tasks-strict.md` exist.
5. Re-run the agent after the OpenCode asset installation is complete.

## Notes

- OpenCode is run in CI mode to avoid interactive prompts.
