# Yolo Runner

Runs OpenCode in YOLO mode against a single bead task at a time. The runner owns task selection, status updates, and logging; the agent only executes the task it is given.

## v2 Migration

The repo now supports split v2 CLIs:

- `yolo-agent` for orchestration
- `yolo-task` for task manager operations
- `yolo-tui` for read-only event monitoring

See `MIGRATION.md` for command mapping and compatibility details.

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

## Test

```
make test
```

## E8 Release Gate Checklist

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

### Stdin GUI Operator Flow

Use streaming mode to drive `yolo-tui` from stdin in real time:

```
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --stream | ./bin/yolo-tui --events-stdin
```

The monitor is decoder-safe: malformed NDJSON lines are surfaced as `decode_error` warnings in the UI and stderr while valid subsequent events continue rendering.

### `yolo-agent` preflight (commit + push first)

Always commit and push ticket/config changes before starting `yolo-agent`.

- Required before run: commit `.tickets/*.md` and related config/code changes, then run `git push`.
- Why: each task runs in a fresh clone that syncs against `origin/main`; local-only commits are not visible in task clones.
- Symptom when skipped: runner output shows errors like `ticket '<id>' not found` in clone context.

Quick preflight:

```
git status --short
git push
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

- `agent.backend` must be one of `opencode`, `codex`, `claude`, `kimi`.
- `agent.concurrency` must be greater than `0`.
- `agent.runner_timeout` must be greater than or equal to `0`.
- `agent.watchdog_timeout` must be greater than `0`.
- `agent.watchdog_interval` must be greater than `0`.
- `agent.retry_budget` must be greater than or equal to `0`.

Invalid config values fail startup with field-specific errors that reference `.yolo-runner/config.yaml`.

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

## Task Prompt

The prompt includes:
- Bead ID and title
- Description
- Acceptance criteria
- Strict TDD rules

The runner selects work by traversing container types (epic, molecule). Traversable containers are in `open` or `in_progress` status, and leaf work is eligible when it is open only.

The YOLO agent must only work on the prompt provided. It must not call beads commands.

## Logging

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

After finishing a batch of tasks, run `bd epic close-eligible` to close epics whose children are complete and keep `bd ready` output clean.

## Failure Modes

- **No changes after OpenCode run**: task is marked `blocked`; no commit or close.
- **Commit fails**: runner exits with the git error; task remains in progress.
- **OpenCode fails**: runner exits with the OpenCode error code.

## Troubleshooting

If the runner refuses to start, the most common cause is a missing agent file or missing `permission: allow` line in `.opencode/agent/yolo.md`. The runner validates the agent at startup and exits when the file is missing or missing agent permissions.

Recovery steps:

1. Run `./bin/yolo-runner init --repo .` to reinstall the agent file.
2. Confirm `.opencode/agent/yolo.md` exists and includes `permission: allow`.
3. Re-run the runner after the agent installation is complete.

## Notes

- OpenCode is run in CI mode to avoid interactive prompts.
- The runner is responsible for `bd close` and `bd sync` after a successful commit.
