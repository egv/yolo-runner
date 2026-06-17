# Watch Supervisor Playbook

Use `yolo-agent watch` for queue-backed production runs that need one process to supervise multiple sources and autoscaled runner pools. It replaces the manual "start source, start runner, start events follow" operator loop for normal Startrek and Arc PR operation.

Keep `yolo-agent source ...`, `yolo-agent runner ...`, and `yolo-agent events follow ...` available for focused debugging, one-off validation, or rollback.

## What Watch Starts

`watch` reads `.yolo-runner/config.yaml`, opens the configured SQLite queue, starts every configured source in-process, then starts each runner pool at `min_replicas`. On each autoscale tick it checks pending and active queue depth per pool and adjusts replicas up to the pool and global limits.

- Sources: `startrek` and `arcpr`.
- Runner pools: named groups with `source`, `presets`, `min_replicas`, `max_replicas`, and `capacity`.
- Autoscale: global `min_runners` and `max_runners` cap total replicas across pools.
- TUI: `--tui` or `watch.tui.default_mode: ui` starts `yolo-tui` and streams events into it.
- Stream mode: `watch.tui.default_mode: stream` writes NDJSON to stdout; combine with `--events` when you also need a JSONL artifact.

Per-preset `limits.max_concurrent` still comes from `~/.yolo-runner/environments.yaml` and is enforced by the existing queue claim logic. A pool can add replicas, but claim-time preset limits still cap concurrent work.

## Preflight

Run this before starting a watch process:

```bash
git status --short
./bin/yolo-agent config validate --repo .
git push
```

Commit and push task definitions, `.yolo-runner/config.yaml`, environment preset changes, and runner code before launch. Sources and runners may create fresh clones or read from `origin/main`; local-only changes are invisible to those workers.

Set the tokens required by enabled sources and presets:

```bash
export STARTREK_TOKEN=<startrek-api-token>
export ARC_TOKEN=<arc-token>
export GITHUB_TOKEN=<github-token> # only when the selected profile or landing path needs GitHub
```

## Config

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
          - key: ADAPTA
            preset: adapta
            root: ~/arcadia/taxi/mobile/adapta
          - key: RIDETECH
            preset: ridetech
            root: ~/arcadia/taxi/mobile/ridetech
  arc-review:
    tracker:
      type: tk

tracker_agent:
  poll_interval: 30s
  labels:
    ready: yolo-agent-ready
    in_progress: yolo-agent-in-progress
    completed: yolo-agent-completed
    blocked: yolo-agent-blocked
    failed: yolo-agent-failed

arc_review_watch:
  poll_interval: 30s
  state_path: .yolo-runner/arcpr-source-state.db
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
      presets: [adapta, ridetech]
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
      subpath: taxi/mobile/adapta
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

  ridetech:
    workspace:
      strategy: arc-shared
      mount: ~/arcadia
      subpath: taxi/mobile/ridetech
    landing:
      type: arc-pr
    agent:
      backend: codex
      model: gpt-5.5
      runner_timeout: 20m
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

The Startrek queue `preset` is optional. If it is omitted, the source falls back to the source/profile preset. Set it per queue when one Startrek profile watches several queues that need different workspaces, landing policy, or model limits.

## Run

Interactive monitor:

```bash
./bin/yolo-agent watch \
  --repo . \
  --environments ~/.yolo-runner/environments.yaml \
  --events "runner-logs/watch-$(date +%Y%m%d_%H%M%S).events.jsonl" \
  --tui
```

Service-style stream:

```bash
./bin/yolo-agent watch \
  --repo . \
  --environments ~/.yolo-runner/environments.yaml \
  --events "runner-logs/watch-$(date +%Y%m%d_%H%M%S).events.jsonl" \
  --stream
```

Autoscaler tuning:

```bash
./bin/yolo-agent watch \
  --repo . \
  --environments ~/.yolo-runner/environments.yaml \
  --tick-interval 2s \
  --idle-cooldown 5m \
  --tui
```

## Manual Split-Process Fallback

Use this when debugging one source or runner pool independently:

```bash
./bin/yolo-agent runner \
  --queue .yolo-runner/watch.db \
  --environments ~/.yolo-runner/environments.yaml \
  --presets adapta,ridetech \
  --runner-id debug-adapta-runner \
  --capacity 1

./bin/yolo-agent source startrek \
  --repo . \
  --profile startrek-adapta \
  --queue .yolo-runner/watch.db

./bin/yolo-agent source arcpr \
  --repo . \
  --profile arc-review \
  --queue .yolo-runner/watch.db

./bin/yolo-agent events follow --since 1h | ./bin/yolo-tui --events-stdin
```

## Recovery

For normal queue-backed runs, restarting `watch` is enough. Expired queue leases are requeued automatically, stale rows in the `runners` table are harmless, and sources reconcile from their external systems plus source state.

Use manual cleanup only after an interrupted operator session or when intentionally abandoning in-flight work:

1. Stop the `yolo-agent watch` process.
2. Remove stale direct-run clones for affected tasks: `.yolo-runner/clones/<task-id>`.
3. If a source-specific state DB should forget prior writebacks, back it up before deletion, for example `.yolo-runner/arcpr-source-state.db`.
4. For Startrek issues, remove `yolo-agent-in-progress`; re-add `yolo-agent-ready` only when the issue should be retried.
5. For Beads tasks, move interrupted items back to open and flush JSONL:

```bash
br update <task-id> --status open
br sync --flush-only
```

Do not clear `.yolo-runner/scheduler-state.json` for queue-backed watch runs; the queue is the source of runtime state.

## Verification

Before landing watch-related config or docs changes:

```bash
go test ./...
./bin/yolo-agent watch --help
./bin/yolo-agent config validate --repo .
```

For command smoke checks without installed binaries:

```bash
go run ./cmd/yolo-agent watch --help
go run ./cmd/yolo-agent watch --tui --help
```
