This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.


## Worktree Policy

Do not use git worktrees for this repo. Work directly on `main` or a single feature branch in the main working directory.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

## yolo-agent Run Preflight (Required)

Before starting `yolo-agent`, make sure task definitions and run configuration are pushed to remote:

1. Commit all relevant local changes (especially `.tickets/*.md`, scheduler/task config, and runner code).
2. Run `git push` successfully.
3. Start `yolo-agent` only after push succeeds.

Why this is required: task clones are created via `git clone` and then synced against `origin/main`. If ticket/config changes exist only in local commits, clones will not see them and tasks can fail with errors like `ticket '<id>' not found`.

If a run is interrupted, reset state before restarting:

1. Stop any running `yolo-agent` process.
2. Move interrupted task(s) back from `in_progress` to `open` if needed.
3. Remove stale clone(s) under `.yolo-runner/clones/<task-id>`.
4. Clear stale `in_flight` entries in `.yolo-runner/scheduler-state.json`.
