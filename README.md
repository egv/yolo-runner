# Yolo Runner

AI-powered task execution system with pluggable storage backends (GitHub, Linear, TK), dependency-aware scheduling, and smart concurrency calculation. The runner owns task selection, status updates, and logging; agents execute tasks they're given.

## Features

- **Pluggable Storage Backends**: GitHub Issues, Linear, or local TK (markdown) tickets
- **Task Engine**: Graph-based scheduler with dependency resolution and parent-child hierarchies
- **Smart Concurrency**: Automatically calculates optimal parallel execution from dependency graphs
- **TDD Mode**: Strict Red/Green/Refactor enforcement for test-driven development
- **Structured Logging**: JSONL event streams with log browser TUI
- **Installation Scripts**: One-line install via `install.sh` or `install.ps1`
- **Auto-Update**: Built-in binary updates from GitHub releases
- **Multi-Backend Support**: OpenCode, Codex, Claude, Kimi

## CLI Tools

- `yolo-agent` - Task orchestration and scheduling
- `yolo-task` - Task management operations
- `yolo-tui` - Real-time event monitoring with log browser
- `yolo-linear-worker` - Linear webhook processor
- `yolo-linear-webhook` - Linear webhook handler

See `MIGRATION.md` for command mapping and compatibility details.

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

### Update Existing Installation

```bash
./bin/yolo-runner update
./bin/yolo-runner update --release v1.2.3  # Pin to specific version
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

## Location

- Canonical script: `tools/yolo-runner/beads_yolo_runner.py`
- Compatibility copy (in-use by existing invocations): `scripts/beads_yolo_runner.py`

## What It Does

- Selects the next open leaf task using beads (`bd ready`).
- Builds a task prompt with title, description, and acceptance criteria.
- Runs OpenCode with the YOLO agent using a single prompt.
- Captures OpenCode JSON output to a per-task log file.
- Commits changes, closes the bead, verifies it is closed, then runs `bd sync`.
- If no code changes were produced, marks the task as `blocked` and exits.

## Requirements

- `bd` (beads) CLI available and initialized.
- `opencode` CLI available.
- `git` installed and repo cloned.
- `uv` installed for the Python runner (bootstrap only).
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
./bin/yolo-runner --version
./bin/yolo-tui --version
```

### Update Binary

```bash
# Update to latest release
./bin/yolo-runner update

# Update to specific version
./bin/yolo-runner update --release v1.2.3

# Update with custom OS/arch
./bin/yolo-runner update --os linux --arch arm64
```

Update flags:
- `--release VERSION` - Target version (default: `latest`)
- `--os OS` - Target OS: `linux`, `darwin`, `windows`
- `--arch ARCH` - Target arch: `amd64`, `arm64`
- `--install-dir PATH` - Custom install directory
- `--release-api URL` - GitHub API base URL

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

## Init

The runner includes a helper to install the YOLO agent file into the OpenCode agent directory.

Init usage:

```
./bin/yolo-runner init --repo .
```

This performs the agent installation step by copying `yolo.md` into `.opencode/agent/yolo.md`.

## Update

Use `update` to refresh `yolo-runner` from GitHub release artifacts.

```bash
./bin/yolo-runner update [flags]
```

Flags:

- `--release` (default `latest`): install the latest release, or pin to a specific tag like `v1.2.3`.
- `--os` (default local OS): target OS (`linux`, `darwin`, `windows`).
- `--arch` (default local architecture): target architecture (`amd64`, `arm64`).
- `--install-dir`: optional destination directory (defaults to platform-specific `~/.local/bin`, or `%LOCALAPPDATA%/yolo-runner/bin` on Windows).
- `--release-api`: base GitHub releases API URL (default `https://api.github.com/repos/egv/yolo-runner`).

Resolution and selection behavior:

- Release resolution uses `/releases/latest` for `latest`, or `/releases/tags/<tag>` for pinned tags.
- The installer selects `yolo-runner_<os>_<arch>.tar.gz` for non-Windows and `yolo-runner_<os>_<arch>.zip` for Windows.
- Checksums are validated from matching `checksums-<artifact>.txt` entries before install.

Install constraints:

- Install is transactional. If any file copy fails, the command rolls back all changes to the previous state.
- The selected destination directory must be writable; updates fail with `not writable` when permissions block extraction or write.
- On Windows, `--install-dir` must be an absolute Windows path (drive/UNC path) or the command fails with `unsupported Windows install path`.
- Ensure the install directory is on `PATH`, or run `./bin/yolo-runner` with the full path.

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
./bin/yolo-runner --repo . --root algi-8bt --model gpt-4o
./bin/yolo-runner --repo . --root algi-8bt --dry-run
```

The runner starts a TUI by default when attached to a TTY. Use `--headless` to disable the TUI and force plain log output.

Common options:
- `--max N` limit number of tasks processed
- `--dry-run` print the task prompt without running OpenCode
- `--headless` disable the TUI (useful for CI or non-TTY runs)
- `--concurrency N` or `--concurrency auto` - Parallel task execution (default: 1)
- `--tdd` enable strict TDD mode (Red/Green/Refactor)
- `--quality-gate` validate task clarity before execution
- `--mode ui|headless` force UI or headless mode
- `--stream` output JSONL events for TUI consumption
- `--events PATH` write events to file
- `--retry-budget N` max retries per task (default: 5)
- `--profile NAME` use tracker profile from config
- `--backend codex|claude|kimi|gemini|opencode` agent backend
- `--model MODEL` model name (e.g., openai/gpt-5.3-codex)
- `--runner-timeout DURATION` per-task timeout (e.g., 20m)

### Distributed dogfooding (queues via Redis/NATS + Podman)

Use the queue-backed transport with Redis or NATS, started via Podman Compose. Services bind to Tailscale (tailnet) addresses for security - only accessible from within your tailnet.

```bash
make build
make distributed-dev-up

export GITHUB_TOKEN=$(gh auth token)

# Get your tailscale IP (or set YOLO_TAILNET_IP in .env)
export YOLO_TAILNET_IP=$(tailscale ip -4)

./bin/yolo-agent \
  --repo . \
  --root <root-id> \
  --profile github \
  --distributed-bus-backend redis \
  --distributed-bus-address "redis://${YOLO_TAILNET_IP}:16379" \
  --stream | ./bin/yolo-tui --events-stdin
```

Switch to NATS by changing the backend and address:

```bash
./bin/yolo-agent \
  --repo . \
  --root <root-id> \
  --profile github \
  --distributed-bus-backend nats \
  --distributed-bus-address "nats://${YOLO_TAILNET_IP}:14222" \
  --stream | ./bin/yolo-tui --events-stdin
```

When done, stop the containers:

```bash
make distributed-dev-down
```

#### Makefile targets for distributed dev

```bash
# Start Redis and NATS containers (bound to tailnet IP)
make distributed-dev-up

# Stop and remove containers with volumes
make distributed-dev-down
```

These targets use `podman compose` with `dev/distributed/docker-compose.yml`. Services bind to `YOLO_TAILNET_IP` (default: `100.85.134.92`) so they're only accessible from your Tailscale network.

#### Web UI for monitoring and control

Start the web UI to monitor task queue, task graph, workers, and send control commands:

```bash
export YOLO_TAILNET_IP=$(tailscale ip -4)

./bin/yolo-webui \
  --repo . \
  --listen "${YOLO_TAILNET_IP}:8080" \
  --distributed-bus-backend redis \
  --distributed-bus-address "redis://${YOLO_TAILNET_IP}:16379" \
  --auth-token "${YOLO_WEBUI_TOKEN:-your-secret-token}"
```

Then open in your browser (only accessible from tailnet):

```
http://<your-tailnet-ip>:8080/?token=your-secret-token
```

Features:
- Real-time task queue visualization
- Task graph with status
- Worker summaries
- Control panel to change task status (blocked, in_progress, closed)
- Run history and triage

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

#### TUI Bus Mode (connect directly to Redis/NATS)

Connect TUI directly to the distributed bus - useful when running agent separately or monitoring remote runs:

```bash
export YOLO_TAILNET_IP=$(tailscale ip -4)

./bin/yolo-tui \
  --repo . \
  --events-bus \
  --events-bus-backend redis \
  --events-bus-address "redis://${YOLO_TAILNET_IP}:16379"
```

**TUI shows:**
- Task queue with pending/ready tasks
- Task graph with dependency tree and statuses
- Worker summaries (active executors)
- Run history and landing/triage outcomes
- Real-time status bar with metrics

**TUI vs Web UI:**
- Use `yolo-tui` for terminal-based monitoring, local or SSH sessions
- Use `yolo-webui` for browser access, remote monitoring, and sending control commands

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
4. Remove stale `in_flight` entries from `.yolo-runner/scheduler-state.json`.

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

- Backend: `--agent-backend > --backend > YOLO_AGENT_BACKEND > agent.backend > opencode`
- Profile: `--profile > YOLO_PROFILE > default_profile > default`
- Model and numeric/duration defaults: CLI flag value wins; if unset, `agent.*` value is used.
- Retry budget defaults to `5` per task when neither `--retry-budget` nor `agent.retry_budget` is set.

Validation rules for `agent.*` values:

- `agent.backend` must be one of `opencode`, `codex`, `claude`, `kimi`, `gemini`.
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
{"type": "runner_output", "task_id": "abc-123", "message": "...", "ts": "2026-02-22T10:00:05Z"}
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

## Legacy Logging

- Runner summary log: `runner-logs/beads_yolo_runner.jsonl`
- Per-task OpenCode logs: `runner-logs/opencode/<issue-id>.jsonl`

## Sample output

```
$ ./bin/yolo-runner --repo . --root algi-8bt --max 1
[runner] selected bead yolo-runner-127.2.5
[runner] starting opencode run
[opencode] running task yolo-runner-127.2.5
[runner] commit created and bead closed
```

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

Use a throwaway branch or a fresh worktree so the run-once flow can safely create a commit and update beads.

1. Create a throwaway branch (or worktree) and ensure the repo is clean.
2. Run `bd ready` and confirm the selected bead is the one you want to exercise end-to-end.
3. Run the runner once, for example: `./bin/yolo-runner --repo . --root <root-id> --max 1`.
4. Inspect the resulting commit and confirm it only includes the expected changes for the bead.
5. Review the logs at `runner-logs/beads_yolo_runner.jsonl` and `runner-logs/opencode/<issue-id>.jsonl` to confirm the run-once flow completed.

Success looks like: the runner finishes without errors, a single commit exists for the bead, the bead is closed and synced, and the logs show a complete OpenCode run with a recorded commit and `bd close`/`bd sync` steps.

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

## Failure Modes

- **No changes after OpenCode run**: task is marked `blocked`; no commit or close.
- **Commit fails**: runner exits with the git error; task remains in progress.
- **OpenCode fails**: runner exits with the OpenCode error code.

## Troubleshooting

### Agent Not Found

```bash
./bin/yolo-runner init --repo .
```

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

# Clear scheduler state
# Edit .yolo-runner/scheduler-state.json and remove stale entries
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

### Legacy Agent Issues

If the runner refuses to start with agent errors:

1. Run `./bin/yolo-runner init --repo .` to reinstall the agent file.
2. Confirm `.opencode/agent/yolo.md` exists and includes `permission: allow`.
3. Re-run the runner after the agent installation is complete.

## Notes

- OpenCode is run in CI mode to avoid interactive prompts.
- The runner is responsible for `bd close` and `bd sync` after a successful commit.
