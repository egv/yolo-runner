# Yolo Runner

Runs OpenCode in YOLO mode against a single bead task at a time. The runner owns task selection, status updates, and logging; the agent only executes the task it is given.

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

## Build

From repo root:

```
make build
```

## Test

```
make test
```

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
