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
            root: .yolo-runner/arc-mounts/vay
            arc_mount:
              enabled: true
              store: .yolo-runner/arc-stores/vay/store
              object_store: .yolo-runner/arc-stores/shared-store
              allow_other: true
              ssh_tokens: true
              inode_cache_size: 100000
              cache_size: 134217728
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
  status_transitions:
    in_progress: inProgress
    completed: closed
    completed_resolution: fixed
landing:
  type: arc-pr
  title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"
```

`startrek-poc` is the watcher profile. The `beads` profile is the local task-management profile used by the PoC epic run.

When `arc_mount.enabled` is true, `root` is the Arc mount path. If `root` is omitted, the watcher uses `.yolo-runner/arc-mounts/<queue-key>`. Before preflight/implementation, the watcher creates the mount with `arc mount`, using the per-queue `store` and shared `object_store`. If it created the mount during the poll, it runs `arc unmount --forget <mount>` when the queue attempt finishes. If the mount already existed, the watcher reuses it and leaves it mounted.

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

## Labels And Status Transitions

The watcher searches each configured Startrek queue for issues with `yolo-agent-ready`. During preflight it removes `yolo-agent-ready`, adds `yolo-agent-in-progress`, then either restores `yolo-agent-ready` or applies the needs-info transition. If at least one task passes preflight, the watcher runs the normal implementation loop for that queue root and persists task status through the configured Startrek labels.

For Startrek issue workflow status, `tracker_agent.status_transitions` maps runner task states to Tracker transition IDs. By default the watcher uses `inProgress` when work starts and `closed` with resolution `fixed` when a task completes. `ready`, `blocked`, and `failed` transitions are disabled by default because many queues do not have generic matching workflow transitions. Set any transition field to an empty string to disable it explicitly.

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

- Arc PR landing requires a working local `arc` CLI. With `arc_mount.enabled`, the watcher creates the Arcadia root before running preflight and implementation; without it, `root` must point to an existing Arcadia checkout/mount.
- Startrek status updates are label-driven in this PoC, so manual label edits can make a task eligible or hidden from the watcher.
- The watcher lock is local to the repo checkout. Multiple checkouts can still run competing watchers if operators start them independently.
- Dry-run mode validates command wiring but intentionally skips Startrek mutations.
