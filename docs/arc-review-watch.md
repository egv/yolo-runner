# Arc Review Watch Operator Runbook

Use this runbook from the yolo-runner repo root when operating `yolo-agent arc-review-watch`.

The watcher uses top-level `.yolo-runner/config.yaml` settings under `arc_review_watch`, emits JSONL run events, and records PR review sessions in a SQLite state file. The current default state path is `.yolo-runner/arc-review-watch-state.json`; treat that file as SQLite even though the suffix is `.json`.

The `--profile` flag is accepted for operator metadata and command consistency. The watch settings are resolved from the top-level `arc_review_watch` block.

## Configuration

Start with shipping disabled. Keep `allow_ship: false` for the first production run so the watcher can discover PRs, create or update review sessions, and prove the review loop without landing anything.

```yaml
profiles:
  arc-dev:
    tracker:
      type: tk
arc_review_watch:
  poll_interval: 30s
  lock_path: .yolo-runner/arc-review-watch.lock
  state_path: .yolo-runner/arc-review-watch-state.json
  max_concurrency: 1
  allow_ship: false
  workspaces:
    - /arcadia/users/operator/review-workspace
  branches:
    - trunk
  arc_mount:
    enabled: true
    mount: .yolo-runner/arc-mounts/review
    store: .yolo-runner/arc-stores/review/store
    object_store: .yolo-runner/arc-stores/shared-store
    allow_other: true
    ssh_tokens: true
    inode_cache_size: 100000
    cache_size: 134217728
    override_lazy_checkout: 0
```

Important fields:

- `poll_interval`: time between watch iterations when `--once` is omitted.
- `lock_path`: local single-watcher lock. Remove it only after confirming no watcher is running.
- `state_path`: SQLite session store for PR sessions, heartbeats, event rows, answered comments, and retry state.
- `max_concurrency`: upper bound for concurrently managed review sessions.
- `allow_ship`: final shipping gate. Omitted or `false` keeps shipping disabled.
- `workspaces` and `branches`: filters used by PR discovery.
- `arc_mount`: optional Arc mount settings used when the watcher needs a managed Arcadia checkout.

Validate the file before a live run:

```bash
./bin/yolo-agent config validate --repo .
```

## Dry Run

Run one iteration before starting a long-lived watcher:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --once --dry-run
```

Dry-run mode loads `.yolo-runner/config.yaml`, resolves `arc_review_watch`, acquires `.yolo-runner/arc-review-watch.lock`, emits start/finish events, and skips SQLite mutations and process restarts. Use it after every config change.

For a dry run with a saved event log:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --once --dry-run --events "runner-logs/arc-review-watch-dry-run.events.jsonl"
```

## Live Events and TUI

Without `--stream` or `--events`, the watcher writes events to `runner-logs/arc-review-watch.events.jsonl`.

For live TUI monitoring while also saving an event log:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

The `run_started` and `run_finished` event metadata includes `command`, `repo`, `profile`, `dry_run`, `once`, `poll_interval`, `lock_path`, `state_path`, `max_concurrency`, and `allow_ship`. PR review runner process output is written under `runner-logs/arc-pr-review-<session-id>.log`.

## Safe Startup

1. Commit and push the config and task definitions needed by any downstream clones.
2. Run `./bin/yolo-agent config validate --repo .`.
3. Run `./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --once --dry-run`.
4. Confirm the event log reports `dry_run=true` and the expected `state_path`.
5. Start the watcher with `allow_ship: false`.
6. Inspect the first live cycle in TUI and verify sessions are being discovered as expected.

Long-lived run:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

## SQLite Inspection and Reset

Inspect current sessions:

```bash
sqlite3 .yolo-runner/arc-review-watch-state.json \
  "SELECT id, pr_id, workspace, branch, status, pid, heartbeat_at, failure_count, log_path FROM pr_sessions ORDER BY updated_at DESC;"
```

Mark a dead running session as crashed so a later iteration can create a replacement:

```bash
sqlite3 .yolo-runner/arc-review-watch-state.json \
  "UPDATE pr_sessions SET status = 'crashed', pid = 0 WHERE id = '<session-id>' AND status = 'running';"
```

Reset a PR completely when the state is wrong and you want fresh discovery:

```bash
sqlite3 .yolo-runner/arc-review-watch-state.json \
  "DELETE FROM pr_sessions WHERE pr_id = '<pr-id>';"
```

Use `DELETE` only for sessions that are not running. Keep the event JSONL and process log paths from `log_path` before deleting if you need audit evidence.

## Stale Process Recovery

When a watcher or child review runner exits midway:

1. Stop the foreground watcher with Ctrl+C or SIGTERM.
2. Confirm remaining processes:

```bash
pgrep -af 'yolo-agent arc-review-watch|arc-pr-review-runner'
```

3. Stop stale child runners only after preserving their logs:

```bash
pkill -f 'arc-pr-review-runner'
```

4. Remove a stale lock only after no watcher remains:

```bash
rm -f .yolo-runner/arc-review-watch.lock
```

5. Inspect SQLite state and mark dead `running` sessions as `crashed`.
6. Restart with `--once --dry-run`; then start the long-lived watcher when config and state look correct.

The watcher also detects stale `running` sessions by heartbeat age or dead PID and creates replacement sessions. Manual reset is for cases where the operator needs to clear a stuck lock, terminate old child processes, or force a known-bad session out of `running`.

## Ship Instructions

Shipping is controlled by `arc_review_watch.allow_ship`. The ship gate still requires a reviewed current revision, no open blockers, no unanswered comments, non-failing checks, and a model verdict of `ship`.

Recommended rollout:

1. First production run: keep `allow_ship: false` and watch at least one full review cycle.
2. Confirm review comments, summaries, checks, and session heartbeats are correct.
3. Confirm no stale `running` sessions remain in SQLite.
4. Set `arc_review_watch.allow_ship: true` explicitly:

```yaml
arc_review_watch:
  allow_ship: true
```

5. Run one more `--once --dry-run` to confirm config parsing and event metadata.
6. Start the live watcher and monitor TUI plus `runner-logs/arc-review-watch.events.jsonl`.

If an unexpected ship attempt is suspected, immediately stop the watcher, set `allow_ship: false`, remove only stale locks/processes, and inspect the SQLite sessions plus event logs before restarting.
