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
- Git repo with clean working tree (recommended).

## Run

From repo root:

```
uv run python tools/yolo-runner/beads_yolo_runner.py --repo . --root algi-8bt
```

Common options:
- `--max N` limit number of tasks processed
- `--dry-run` print the task prompt without running OpenCode

## Task Prompt

The prompt includes:
- Bead ID and title
- Description
- Acceptance criteria
- Strict TDD rules

The YOLO agent must only work on the prompt provided. It must not call beads commands.

## Logging

- Runner summary log: `runner-logs/beads_yolo_runner.jsonl`
- Per-task OpenCode logs: `runner-logs/opencode/<issue-id>.jsonl`

## Failure Modes

- **No changes after OpenCode run**: task is marked `blocked`; no commit or close.
- **Commit fails**: runner exits with the git error; task remains in progress.
- **OpenCode fails**: runner exits with the OpenCode error code.

## Notes

- OpenCode is run in CI mode to avoid interactive prompts.
- The runner is responsible for `bd close` and `bd sync` after a successful commit.
