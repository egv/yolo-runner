# Tracker Agent PoC Operator Runbook

Use this runbook to operate the tracker-agent PoC from the yolo-runner repo root. The PoC root epic ID is `yolo-tracker-agent-poc-vay`.

## Configuration

Configure the tracker profile, tracker-agent polling labels, and Arc PR landing in `.yolo-runner/config.yaml`.

```yaml
default_profile: beads
profiles:
  beads:
    tracker:
      type: beads
  startrek-poc:
    tracker:
      type: startrek
      startrek:
        endpoint: https://st-api.example.test
        token_env: STARTREK_TOKEN
        queues:
          - key: VAY
            root: /path/to/arcadia/project
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 3
  runner_timeout: 20m
  watchdog_timeout: 10m
  watchdog_interval: 5s
  retry_budget: 5
tracker_agent:
  poll_interval: 30s
  lock_path: .yolo-runner/tracker-agent.lock
  labels:
    ready: yolo-agent-ready
    in_progress: yolo-agent-in-progress
    completed: yolo-agent-completed
    blocked: yolo-agent-blocked
    failed: yolo-agent-failed
landing:
  type: arc-pr
  title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"
```

`startrek-poc` is the watcher profile. The `beads` profile is the local task-management profile used by the PoC epic run.

## Environment

Set only the tokens required by the profiles you use:

```bash
export STARTREK_TOKEN=<startrek-api-token>
export GITHUB_TOKEN=<github-token>
export YOLO_AGENT_BACKEND=codex
```

`STARTREK_TOKEN` is required for `tracker-watch` with the `startrek-poc` profile. `GITHUB_TOKEN` is required only when the selected tracker profile or surrounding workflow uses GitHub. Arc PR landing uses the local `arc` CLI and an Arcadia root from the queue config.

## Dry Run

Verify the watcher can load config and acquire the lock without mutating Startrek labels:

```bash
./bin/yolo-agent tracker-watch --repo . --profile startrek-poc --once --dry-run
```

Use `--once` for manual checks. Omit `--once` only when running the watcher as a long-lived process.

## Run

Run the PoC epic through the beads profile:

```bash
./bin/yolo-agent --repo . --root yolo-tracker-agent-poc-vay --profile beads --agent-backend codex --model openai/gpt-5.3-codex --concurrency 3 --runner-timeout 20m --watchdog-timeout 10m --watchdog-interval 5s --events "runner-logs/yolo-tracker-agent-poc-vay-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

For a config-driven run, keep the same root and profile but rely on `.yolo-runner/config.yaml` defaults:

```bash
./bin/yolo-agent --repo . --root yolo-tracker-agent-poc-vay --profile beads --stream | ./bin/yolo-tui --events-stdin
```

Before either run, commit and push the task/config changes that task clones must see.

## Labels

The watcher searches each configured Startrek queue for issues with `yolo-agent-ready`. During preflight it removes `yolo-agent-ready`, adds `yolo-agent-in-progress`, then either restores `yolo-agent-ready` or applies the needs-info transition. If at least one task passes preflight, the watcher runs the normal implementation loop for that queue root and persists task status through the configured Startrek labels.

Default labels:

- `yolo-agent-ready`
- `yolo-agent-in-progress`
- `yolo-agent-completed`
- `yolo-agent-blocked`
- `yolo-agent-failed`
- `needs-info`

Keep label names stable across watcher and operator reset steps. If a custom label is configured under `tracker_agent.labels`, use that configured value everywhere.

## Reset Procedure

Use this reset when an operator stops a run or a watcher process exits midway:

1. Stop `yolo-agent` and any long-lived `yolo-agent tracker-watch` process.
2. Remove stale lock files: `rm -f .yolo-runner/tracker-agent.lock`.
3. For interrupted Startrek issues, remove `yolo-agent-in-progress`; re-add `yolo-agent-ready` only when the issue should be retried.
4. If a preflight question was posted, leave `needs-info` in place until the author replies.
5. Reset interrupted beads tasks to open, then flush local bead state:

```bash
br update <task-id> --status open
br sync --flush-only
```

6. Remove stale runner clones and scheduler state for interrupted tasks:

```bash
rm -rf .yolo-runner/clones/<task-id>
```

If `.yolo-runner/scheduler-state.json` exists and contains a stale `in_flight` entry for the interrupted task, remove only that entry.

## Known Limitations

- Arc PR landing requires a working local `arc` CLI and a valid Arcadia root per Startrek queue.
- Startrek status updates are label-driven in this PoC, so manual label edits can make a task eligible or hidden from the watcher.
- The watcher lock is local to the repo checkout. Multiple checkouts can still run competing watchers if operators start them independently.
- Dry-run mode validates command wiring but intentionally skips Startrek mutations.
