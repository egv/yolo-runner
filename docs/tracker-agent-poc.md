# Tracker Agent PoC Operator Runbook

Use this runbook to operate the tracker-agent PoC from the yolo-runner repo root. The PoC root epic ID is `yolo-tracker-agent-poc-vay`.

For production-style queue-backed operation, prefer `yolo-agent watch`; it starts the Startrek source and runner pools in one process and can open the TUI directly. The legacy direct epic command remains useful for local beads-only validation, and the split `source`/`runner` commands remain useful for debugging.

## Configuration

Configure the tracker profile, the per-queue discovery trigger, the tracker-agent polling, and Arc PR landing in `.yolo-runner/config.yaml`.

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
            assignee: genaevstratov       # required: pick up issues assigned to this Startrek user
            label: yolo-agent-ready       # optional, defaults to yolo-agent-ready
            preset: startrek-poc
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
  status_transitions:
    in_progress: inProgress
    completed: closed
    completed_resolution: fixed
landing:
  type: arc-pr
  title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"
watch:
  queue_path: .yolo-runner/watch.db
  sources:
    - name: startrek-poc
      type: startrek
      profile: startrek-poc
  runner_pools:
    - name: startrek-poc-runners
      source: startrek-poc
      presets: [startrek-poc]
      min_replicas: 1
      max_replicas: 3
      capacity: 1
  autoscale:
    min_runners: 1
    max_runners: 3
  tui:
    default_mode: stream
```

`startrek-poc` is the watcher profile. The `beads` profile is the local task-management profile used by the PoC epic run.

`queues[].preset` routes each Startrek queue to a runner preset. Use it when one Startrek profile watches several queues that need different Arcadia subpaths, landing policy, model choice, or concurrency limits. If omitted, the Startrek source falls back to the source/profile preset.

When `arc_mount.enabled` is true, `root` is the Arc mount path. If `root` is omitted, the watcher uses `.yolo-runner/arc-mounts/<queue-key>`. Before preflight/implementation, the watcher creates the mount with `arc mount`, using the per-queue `store` and shared `object_store`. If it created the mount during the poll, it runs `arc unmount --forget <mount>` when the queue attempt finishes. If the mount already existed, the watcher reuses it and leaves it mounted.

## Environment

Set only the tokens required by the profiles you use:

```bash
export STARTREK_TOKEN=<startrek-api-token>
export GITHUB_TOKEN=<github-token>
export YOLO_AGENT_BACKEND=codex
```

`STARTREK_TOKEN` is required for `watch`, `source startrek`, and `tracker-watch` with the `startrek-poc` profile. `GITHUB_TOKEN` is required only when the selected tracker profile or surrounding workflow uses GitHub. Arc PR landing uses the local `arc` CLI and an Arcadia root from the queue config.

## Dry Run

Verify the watcher can load config and acquire the lock without mutating Startrek labels:

```bash
./bin/yolo-agent tracker-watch --repo . --profile startrek-poc --once --dry-run
```

Use `--once` for manual checks. Omit `--once` only when running the watcher as a long-lived process.

## Watch Supervisor Run

Validate config, then start the source and runner pool from one process:

```bash
./bin/yolo-agent config validate --repo .
./bin/yolo-agent watch \
  --repo . \
  --environments ~/.yolo-runner/environments.yaml \
  --events "runner-logs/startrek-poc-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" \
  --tui
```

Use `--stream` instead of `--tui` for service logs or when another process will consume NDJSON.

Before the run, commit and push the task/config changes that task clones must see.

## Direct Beads Epic Run

Run the PoC epic through the beads profile:

```bash
./bin/yolo-agent --repo . --root yolo-tracker-agent-poc-vay --profile beads --agent-backend codex --model openai/gpt-5.3-codex --concurrency 3 --runner-timeout 20m --watchdog-timeout 10m --watchdog-interval 5s --events "runner-logs/yolo-tracker-agent-poc-vay-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

For a config-driven run, keep the same root and profile but rely on `.yolo-runner/config.yaml` defaults:

```bash
./bin/yolo-agent --repo . --root yolo-tracker-agent-poc-vay --profile beads --stream | ./bin/yolo-tui --events-stdin
```

## Queue-Split Manual Run

`tracker-watch` is a deprecated compatibility shim. `watch` is the preferred queue-backed operator path. Use the manual split-process form when debugging the Startrek source or a runner independently:

```bash
# 1. Define a preset named after the profile in ~/.yolo-runner/environments.yaml
#    (see docs/environment-presets.md). 2. Start a runner that serves it:
./bin/yolo-agent runner --queue .yolo-runner/watch.db \
  --environments ~/.yolo-runner/environments.yaml --presets startrek-poc

# 3. Start the Startrek source: it polls the queue, submits preflight/split/implement
#    work items, and writes results back (labels, transitions, comments, subtasks):
./bin/yolo-agent source startrek --repo . --profile startrek-poc --queue .yolo-runner/watch.db

# 4. Watch the merged event stream:
./bin/yolo-agent events follow | ./bin/yolo-tui --events-stdin
```

The source owns all Startrek semantics (the labels and transitions below); the runner owns workspace isolation (arc mount preparation moves to the preset's `arc-shared` materialization) and execution. Queue leases replace `scheduler-state.json` recovery.

## Discovery Trigger And Status

The watcher searches each configured Startrek queue for issues that are **assigned to the queue's `assignee`** and **carry the queue's `label`** (default `yolo-agent-ready`). Both conditions must hold — this is the sole opt-in signal. During preflight it removes the label, adds `yolo-agent-in-progress`, then either restores the label or applies the needs-info transition.

Task status is read from the **native Startrek workflow status**, not from labels: an issue is runnable only when its workflow status maps to Open, and it drops out of the candidate set once transitioned to In Progress or Closed. The four legacy status labels (`yolo-agent-in-progress`/`-completed`/`-blocked`/`-failed`) are gone. Only the operational labels remain: `yolo-agent-ready` (the opt-in scope marker), `yolo-agent-in-progress` (the preflight/processing marker), `needs-info`, and `agent:subtask`.

For Startrek issue workflow status, `tracker_agent.status_transitions` maps runner task states to Tracker transition IDs. By default the watcher uses `inProgress` when work starts and `closed` with resolution `fixed` when a task completes. `ready`, `blocked`, and `failed` transitions are disabled by default because many queues do not have generic matching workflow transitions. Set any transition field to an empty string to disable it explicitly.

Operational labels:

- `yolo-agent-ready` — the discovery opt-in tag (configurable per queue via `label`, defaults to this name)
- `yolo-agent-in-progress` — transient preflight/processing marker, added then removed by the source
- `needs-info` — marks an issue awaiting author input
- `agent:subtask` — marks a generated split subtask

Keep label names stable across watcher and operator reset steps. If a custom label is configured per queue (`queues[].label`), use that configured value everywhere.

## Reset Procedure

Use this reset when an operator stops a run or a watcher process exits midway:

1. Stop `yolo-agent watch` and any long-lived `yolo-agent tracker-watch` or `source startrek` process.
2. Remove stale lock files: `rm -f .yolo-runner/tracker-agent.lock`.
3. For interrupted Startrek issues, remove `yolo-agent-in-progress`; re-add the queue's label (default `yolo-agent-ready`) only when the issue should be retried.
4. If a preflight question was posted, leave `needs-info` in place until the author replies.
5. Reset interrupted beads tasks to open, then flush local bead state:

```bash
br update <task-id> --status open
br sync --flush-only
```

6. Remove stale runner clones for interrupted tasks:

```bash
rm -rf .yolo-runner/clones/<task-id>
```

Queue-backed runs recover from queue leases, and the tracker source reconciles from Startrek on restart. `scheduler-state.json` is no longer written.

## Known Limitations

- Arc PR landing requires a working local `arc` CLI. With `arc_mount.enabled`, the watcher creates the Arcadia root before running preflight and implementation; without it, `root` must point to an existing Arcadia checkout/mount.
- Discovery requires the issue to be assigned to the configured `assignee`; unassigned or differently-assigned issues are invisible to the watcher even if they carry the label.
- The watcher lock is local to the repo checkout. Multiple checkouts can still run competing watchers if operators start them independently.
- Dry-run mode validates command wiring but intentionally skips Startrek mutations.
