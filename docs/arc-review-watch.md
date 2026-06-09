# Arc Review Watch Operator Runbook

Use this runbook from the yolo-runner repo root when operating `yolo-agent arc-review-watch`. Start with a dry run and shipping disabled, confirm event output, then decide whether to run the watcher continuously.

## Configuration

Configure the watcher in `.yolo-runner/config.yaml`.

```yaml
default_profile: arc-review
profiles:
  arc-review:
    tracker:
      type: tk
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 1
  runner_timeout: 20m
  watchdog_timeout: 10m
  watchdog_interval: 5s
arc_review_watch:
  poll_interval: 30s
  lock_path: .yolo-runner/arc-review-watch.lock
  state_path: .yolo-runner/arc-review-watch-state.json
  max_concurrency: 1
  allow_ship: false
  workspaces:
    - /arcadia/users/alice/review-1
  branches:
    - users/alice/review-1
```

Keep `allow_ship: false` for the first production-shaped run. Only switch it to `true` after operators have verified discovery, event output, child process logs, and reset steps in the target repo.

`state_path` is a SQLite state database used for PR sessions, heartbeats, process IDs, and log paths. The default path is `.yolo-runner/arc-review-watch-state.json`; treat it as SQLite state even though the default suffix is `.json`.

Optional `arc_review_watch.arc_mount` fields use the same Arc mount shape as the tracker-agent config:

```yaml
arc_review_watch:
  arc_mount:
    enabled: true
    mount: /tmp/arcadia
    store: /tmp/arc-store
    object_store: /tmp/arc-objects
    allow_other: true
    ssh_tokens: true
    inode_cache_size: 100000
    cache_size: 134217728
```

## Dry Run

Run one polling iteration before allowing any state changes:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run
```

The dry run validates config loading, lock acquisition, and PR discovery wiring. It intentionally skips SQLite session reconciliation and child process startup.

## Events And TUI

For an operator-visible first run, write an event log and stream the same events into the TUI:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

For a non-dry one-shot validation, remove `--dry-run` but keep `--once` and keep `allow_ship: false`:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

When `--events` is omitted and `--stream` is not set, the watcher writes `runner-logs/arc-review-watch.events.jsonl`.

Child PR review processes write their combined stdout/stderr to:

```text
runner-logs/arc-pr-review-<session-id>.log
```

## Continuous Run

After the dry run and one-shot validation pass, start the long-lived watcher by omitting `--once`:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

Keep one watcher per repo checkout. The lock file blocks another watcher in the same checkout, but it does not coordinate with separate clones.

## Stale Process Recovery

Use this when the watcher exits mid-run, the machine reboots, or a PR review child process stops heartbeating.

1. Stop the long-lived watcher.
2. Inspect running sessions and child PIDs:

```bash
STATE=.yolo-runner/arc-review-watch-state.json
sqlite3 "$STATE" "select id, pr_id, pid, status, heartbeat_at, log_path from pr_sessions where status = 'running';"
```

3. For any live but unwanted child, stop only that process:

```bash
kill <pid>
```

4. Confirm no matching child process remains:

```bash
pgrep -af arc-pr-review-runner || true
```

5. Remove the local watcher lock:

```bash
rm -f .yolo-runner/arc-review-watch.lock
```

6. Restart the watcher. Stale `running` sessions with old heartbeats or dead PIDs are marked `crashed`, and replacement sessions are started from the same PR metadata.

## SQLite Reset

Use a full SQLite reset only when the state database is corrupt, points at the wrong repo, or contains sessions that should not be retried. Prefer stale process recovery when the session history is still useful.

1. Stop the watcher and child `arc-pr-review-runner` processes.
2. Back up the state database:

```bash
mv .yolo-runner/arc-review-watch-state.json ".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak"
```

3. Remove the lock:

```bash
rm -f .yolo-runner/arc-review-watch.lock
```

4. Restart with a one-shot dry run:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run
```

The next non-dry run recreates the SQLite schema and reconciles currently discovered PRs into fresh pending sessions.

## Safe Startup Checklist

- Config validates with `allow_ship: false`.
- Dry run succeeds with `--once --dry-run`.
- Event streaming works through `--stream | ./bin/yolo-tui --events-stdin`.
- `runner-logs/arc-review-watch.events.jsonl` or the configured `--events` file is present.
- Child logs appear under `runner-logs/arc-pr-review-<session-id>.log` after the first non-dry one-shot run.
- Operators know how to inspect SQLite state with `sqlite3 "$STATE"` and remove `.yolo-runner/arc-review-watch.lock`.
