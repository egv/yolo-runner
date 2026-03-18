This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.


## Task Management Backends

This project supports multiple task tracking backends:

### Classic Beads (bd) - Legacy

The original Python-based beads CLI. Still available but legacy.

```bash
# Check if bd is available
which bd

# Typical bd commands
bd ready --parent <epic-id> --json
bd show <task-id> --json
bd update <task-id> --status in_progress
bd close <task-id>
bd sync
```

### Beads Rust (br) - Recommended

Fast Rust port of beads with SQLite + JSONL storage. This is the preferred backend.

**Installation:**
```bash
# Quick install
curl -fsSL "https://raw.githubusercontent.com/Dicklesworthstone/beads_rust/main/install.sh?$(date +%s)" | bash

# Or from source
cargo install --git https://github.com/Dicklesworthstone/beads_rust.git
```

**Initialize in this repo:**
```bash
br init
# Creates .beads/ directory with beads.db, issues.jsonl, config.yaml
```

**Common br commands:**
```bash
# List ready tasks (open, unblocked, not deferred)
br ready --parent <epic-id> --json

# Show task details
br show <task-id> --json

# Update task status
br update <task-id> --status in_progress

# Close a task with reason
br close <task-id> --reason "Implementation complete"

# Sync database to JSONL for git
br sync --flush-only

# List all open tasks in an epic
br ready --parent <epic-id> --json

# Create new task
br create "Task title" --type task --priority 1 --parent <epic-id>

# Mark epic as closed when all children done
br epic close-eligible
```

**Key differences from bd:**
- `br` uses `--flush-only` for sync (bd doesn't require flag)
- `br` output format is compatible with bd (JSON array of issues)
- `br` stores data in SQLite with JSONL export for git
- `br` is faster and has no daemon

**Project configuration:**
The `.beads/config.yaml` controls the ID prefix and defaults:
```yaml
id:
  prefix: "yolo-runner"
defaults:
  priority: 2
  type: "task"
```


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

## yolo-runner / yolo-agent Examples

Prefer `yolo-agent` for new runs (legacy `yolo-runner` remains compatibility mode).

### GitHub profile + TUI streaming

```bash
export GITHUB_TOKEN=<github-personal-access-token>

./bin/yolo-agent \
  --repo . \
  --root <github-epic-issue-number> \
  --profile github \
  --agent-backend codex \
  --model gpt-5.3-codex-spark \
  --concurrency 3 \
  --runner-timeout 20m \
  --watchdog-timeout 10m \
  --watchdog-interval 5s \
  --events "runner-logs/github-$(date +%Y%m%d_%H%M%S).events.jsonl" \
  --stream | ./bin/yolo-tui --events-stdin
```

### Config-driven run (uses `.yolo-runner/config.yaml` defaults)

```bash
export GITHUB_TOKEN=<github-personal-access-token>

./bin/yolo-agent \
  --repo . \
  --root <github-epic-issue-number> \
  --stream | ./bin/yolo-tui --events-stdin
```

### Legacy compatibility command

```bash
./bin/yolo-runner --repo . --root <root-id> --model gpt-5.3-codex-spark
```
