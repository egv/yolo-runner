# Arc Review Watch Operator Runbook

Use this runbook from the yolo-runner repo root when operating `yolo-agent arc-review-watch`. Start with shipping disabled, confirm a dry run and event output, then decide whether to run the watcher continuously.

The watcher reads the top-level `arc_review_watch` block from `.yolo-runner/config.yaml`, emits JSONL run events, and records PR review sessions, answered comments, and reviewed revisions in SQLite state. The current default state path is `.yolo-runner/arc-review-watch-state.json`; treat that file as SQLite even though the suffix is `.json`.

## Configuration

Keep `allow_ship: false` for the first production run so the watcher can discover PRs, create or update review sessions, and prove session handoff without landing anything.

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
  reviewer: alice
  max_concurrency: 1
  allow_ship: false
  workspaces:
    - /arcadia/users/alice/review-1
  branches:
    - users/alice/review-1
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

Important fields:

- `poll_interval`: time between watch iterations when `--once` is omitted.
- `lock_path`: local single-watcher lock. Remove it only after confirming no watcher is running.
- `state_path`: SQLite session store for PR sessions, heartbeats, process IDs, event rows, and log paths.
- `reviewer`: required reviewer identity used by PR discovery. Omitted or blank discovers no eligible PRs.
- `max_concurrency`: upper bound for concurrently managed review sessions.
- `allow_ship`: final shipping gate. Omitted or `false` keeps shipping disabled.
- `workspaces` and `branches`: filters used by PR discovery. Discovery keeps only open PRs assigned to `reviewer` on one of the configured target branches.
- `arc_mount`: optional Arc mount settings used when the watcher needs a managed Arcadia checkout.

Validate the file before a live run:

```bash
./bin/yolo-agent config validate --repo .
```

## Dry Run

Run one polling iteration before allowing any state changes:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run
```

Dry-run mode validates config loading, lock acquisition, and PR discovery wiring. It emits start and finish events and intentionally skips SQLite session reconciliation and child process startup.

For a dry run with a saved event log:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run --events "runner-logs/arc-review-watch-dry-run.events.jsonl"
```

## Events And TUI

When `--events` is omitted and `--stream` is not set, the watcher writes `runner-logs/arc-review-watch.events.jsonl`.

For live TUI monitoring while also saving an event log:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

For an operator-visible one-shot validation, keep `--once` and keep `allow_ship: false`:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

The `run_started` and `run_finished` event metadata includes `command`, `repo`, `profile`, `dry_run`, `once`, `poll_interval`, `lock_path`, `state_path`, `max_concurrency`, and `allow_ship`. Child PR review processes write combined stdout and stderr to `runner-logs/arc-pr-review-<session-id>.log`.

## Watcher Handoff And Live Review Cycle

The watcher polls configured `workspaces`, filters discovered PRs by `reviewer`, `branches`, and open status, deduplicates by PR ID, reconciles the result into `pr_sessions`, and restarts stale `running` sessions. Newly discovered PRs are recorded with their workspace, branch, revision, and `pending` status.

When the watcher starts a child runner, it passes `--state-path <state_path>`, `--events <events_path>`, and passes the setting to child runners as `--allow-ship=<true|false>`. The watcher hands `arc_review_watch.allow_ship` to the child runner as `--allow-ship=true` or `--allow-ship=false`. The child runner also receives `--reviewer <login>` when `arc_review_watch.reviewer` is set, so its model metadata and logs match the discovery filter.

Watcher-started child processes currently include `--once`, so they write one heartbeat and exit instead of running the full live review loop. Treat the watcher path as discovery, SQLite reconciliation, heartbeat, liveness, and process-handoff validation until the child launch mode changes.

Run `arc-pr-review-runner` without `--once` for a full live review cycle. From an existing session:

```bash
./bin/yolo-agent arc-pr-review-runner \
  --repo . \
  --workspace /arcadia/users/alice/review-1 \
  --pr-id <pr-id> \
  --session-id <session-id> \
  --state-path .yolo-runner/arc-review-watch-state.json \
  --events runner-logs/arc-review-watch.events.jsonl \
  --reviewer alice \
  --allow-ship=false
```

Each live cycle writes a heartbeat, fetches the current PR runtime state, compares the current revision with `reviewed_revisions`, and plans one action before waiting for the next poll interval unless the PR has reached a terminal state:

- Review: for a new revision, open blockers, or failed checks, run the configured model, post inline comments plus a summary, and store the reviewed revision in `reviewed_revisions`.
- Answer: for unanswered comments, run the reply path and post answers.
- Wait: for pending or unknown checks, terminal PR state, or disabled shipping after review, leave the PR untouched until the next interval.
- Ship: call the ship gate only after `allow_ship: true`, a reviewed current revision, no open blockers, no unanswered comments, non-failing checks, and a model verdict of `ship`.

## Safe Startup

1. Commit and push the config and task definitions needed by any downstream clones.
2. Run `./bin/yolo-agent config validate --repo .`.
3. Run `./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run`.
4. Confirm the event log reports `dry_run=true` and the expected `state_path`.
5. Start the watcher with `allow_ship: false`.
6. Inspect the first non-dry watcher iteration in TUI and verify sessions are being discovered as expected.

After the dry run and one-shot validation pass, start the long-lived watcher by omitting `--once`:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --events "runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

Keep one watcher per repo checkout. The lock file blocks another watcher in the same checkout, but it does not coordinate with separate clones.

## SQLite Inspection And Reset

Inspect current sessions:

```bash
STATE=.yolo-runner/arc-review-watch-state.json
sqlite3 "$STATE" "select id, pr_id, workspace, branch, status, pid, heartbeat_at, failure_count, log_path from pr_sessions order by updated_at desc;"
```

Inspect reviewed revisions used by the live review runner and ship gate:

```bash
sqlite3 "$STATE" "select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;"
```

Mark a dead running session as crashed so a later iteration can create a replacement:

```bash
sqlite3 "$STATE" "update pr_sessions set status = 'crashed', pid = 0 where id = '<session-id>' and status = 'running';"
```

Reset a PR completely when the state is wrong and you want fresh discovery:

```bash
sqlite3 "$STATE" "delete from reviewed_revisions where pr_id = '<pr-id>';"
sqlite3 "$STATE" "delete from pr_sessions where pr_id = '<pr-id>';"
```

Use `delete` only for sessions that are not running. Keep the event JSONL and process log paths from `log_path` before deleting if you need audit evidence. Delete the matching `reviewed_revisions` row when you need the next full runner cycle to review the current revision again.

Use a full SQLite reset only when the state database is corrupt, points at the wrong repo, or contains sessions that should not be retried:

```bash
mv .yolo-runner/arc-review-watch-state.json ".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak"
rm -f .yolo-runner/arc-review-watch.lock
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run
```

The next non-dry run recreates the SQLite schema and reconciles currently discovered PRs into fresh pending sessions.

## Stale Process Recovery

Use this when the watcher exits mid-run, the machine reboots, or a PR review child process stops heartbeating.

1. Stop the long-lived watcher with Ctrl+C or SIGTERM.
2. Inspect running sessions and child PIDs:

```bash
STATE=.yolo-runner/arc-review-watch-state.json
sqlite3 "$STATE" "select id, pr_id, pid, status, heartbeat_at, log_path from pr_sessions where status = 'running';"
```

3. Confirm remaining processes:

```bash
pgrep -af 'yolo-agent arc-review-watch|arc-pr-review-runner' || true
```

4. For any live but unwanted child, stop only that process:

```bash
kill <pid>
```

5. Stop stale child runners only after preserving their logs:

```bash
pkill -f 'arc-pr-review-runner'
```

6. Remove the local watcher lock only after no watcher remains:

```bash
rm -f .yolo-runner/arc-review-watch.lock
```

7. Mark dead `running` sessions as `crashed` or back up the SQLite state if it is corrupt.
8. Restart with `--once --dry-run`; then start the long-lived watcher when config and state look correct.

The watcher also detects stale `running` sessions by heartbeat age or dead PID and creates replacement sessions. Manual reset is for cases where the operator needs to clear a stuck lock, terminate old child processes, or force a known-bad session out of `running`.

## Ship Instructions

Shipping is controlled by `arc_review_watch.allow_ship`. The watcher passes the setting to child runners as `--allow-ship=<true|false>`, and the full live runner also resolves `arc_review_watch.allow_ship` before planning ship actions. The ship gate still requires a reviewed current revision, no open blockers, no unanswered comments, non-failing checks, and a model verdict of `ship`.

The watcher hands `arc_review_watch.allow_ship` to the child runner as `--allow-ship=true` or `--allow-ship=false`. Confirm the child runner log or process arguments show `--allow-ship=true` only after the rollout gate has been intentionally opened.

Recommended rollout:

1. First production run: keep `allow_ship: false` and watch at least one discovery and heartbeat handoff.
2. Run or observe the full `arc-pr-review-runner` loop without `--once` while `allow_ship: false`, then confirm review comments, summaries, checks, and session heartbeats are correct.
3. Confirm no stale `running` sessions remain in SQLite.
4. Set `arc_review_watch.allow_ship: true` explicitly:

```yaml
arc_review_watch:
  allow_ship: true
```

5. Run one more `--once --dry-run` to confirm config parsing and event metadata.
6. Start the long-lived watcher and monitor TUI plus `runner-logs/arc-review-watch.events.jsonl`.

If an unexpected ship attempt is suspected, immediately stop the watcher, set `allow_ship: false`, remove only stale locks/processes, and inspect the SQLite sessions plus event logs before restarting.
