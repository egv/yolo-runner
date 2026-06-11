# Arc Review Watch Operator Runbook

Use this runbook from the yolo-runner repo root when operating `yolo-agent arc-review-watch`. Start with shipping disabled, confirm a dry run and event output, then decide whether to run the watcher continuously.

The watcher reads the top-level `arc_review_watch` block from `.yolo-runner/config.yaml`, emits JSONL run events, and records PR review sessions, answered comments, and reviewed revisions in SQLite state. The current default state path is `.yolo-runner/arc-review-watch-state.json`; treat that file as SQLite even though the suffix is `.json`.

## Configuration

Keep `allow_ship: false` for the first production run so the watcher can discover PRs, create or update review sessions, and prove the live review cycle without landing anything.

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
- `reviewer`: optional reviewer login used to filter discovered PRs and handed to child runners as `--reviewer <login>`. Omit it only when every eligible PR in the configured workspaces should be considered.
- `max_concurrency`: upper bound for concurrently managed review sessions.
- `allow_ship`: watcher-managed shipping handoff. Omitted or `false` makes watcher-started children receive `--allow-ship=false`; they may still review and answer comments, but shipping remains blocked.
- `workspaces` and `branches`: filters used by PR discovery.
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

## Live Review Cycle

A non-dry polling iteration discovers eligible open PRs from the configured `workspaces`, filters them by `reviewer` and `branches`, deduplicates by PR ID, and reconciles the result into `pr_sessions`. Newly discovered PRs are recorded with their workspace, branch, revision, and session status so the watcher can supervise the review runner for that PR.

Eligible sessions are handed to `arc-pr-review-runner` with `--session-id`, `--state-path`, `--events`, and `--allow-ship=true` or `--allow-ship=false`. The child runner also receives `--reviewer <login>` when `arc_review_watch.reviewer` is set, which keeps handoff arguments aligned with discovery filtering. The watcher supervises runner sessions by heartbeat and PID; stale or dead sessions are marked `crashed`, then a replacement session is started with the same handoff contract.

Each runner cycle writes a heartbeat, fetches PR runtime state from Arcanum, reads the last handled revision from `reviewed_revisions`, and plans exactly one action. After a non-terminal action, the runner waits for `poll_interval` and repeats until the PR reaches a terminal state or the process is stopped:

- `review`: run the configured model, post inline comments and a summary, then store the current revision in `reviewed_revisions`.
- `answer`: run the configured model, post replies for unanswered PR comments, then record answered comment IDs.
- `ship`: call the ship gate only after `allow_ship` is true, the current revision has been reviewed, comments and blockers are clear, checks are not failing or pending, and the model verdict is `ship`.
- `wait`: leave the PR untouched when it is terminal, checks are still pending or unknown, or shipping remains disabled after review.

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

## Safe Startup

1. Commit and push the config and task definitions needed by any downstream clones.
2. Run `./bin/yolo-agent config validate --repo .`.
3. Run `./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run`.
4. Confirm the event log reports `dry_run=true` and the expected `state_path`.
5. Start the watcher with `allow_ship: false`.
6. Inspect the first live cycle in TUI and verify sessions are being discovered as expected.

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

Inspect the last reviewed revision per PR:

```bash
sqlite3 "$STATE" "select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;"
```

`reviewed_revisions` is read before planning and written after a successful `review` action posts inline comments and the review summary. Use it to confirm the current PR revision has been reviewed before expecting a later `ship` action to pass the gate.

Mark a dead running session as crashed so a later iteration can create a replacement:

```bash
sqlite3 "$STATE" "update pr_sessions set status = 'crashed', pid = 0 where id = '<session-id>' and status = 'running';"
```

Reset a PR completely when the state is wrong and you want fresh discovery:

```bash
sqlite3 "$STATE" "delete from pr_sessions where pr_id = '<pr-id>';"
```

Use `delete` only for sessions that are not running. Keep the event JSONL and process log paths from `log_path` before deleting if you need audit evidence.

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

For watcher-managed sessions, shipping is controlled by the `arc_review_watch.allow_ship` handoff. The ship gate still requires a reviewed current revision, no open blockers, no unanswered comments, non-failing checks, and a model verdict of `ship`.

The watcher hands `arc_review_watch.allow_ship` to the child runner as `--allow-ship=true` or `--allow-ship=false`. Confirm the child runner log or process arguments show `--allow-ship=true` only after the rollout gate has been intentionally opened.

With `allow_ship: false`, the runner may still review and answer comments, but the ship gate reports shipping disabled. With `allow_ship: true`, shipping can proceed only after the same runner has reviewed the current revision and all other gate conditions pass.

Recommended rollout:

1. First production run: keep `allow_ship: false` and watch at least one live watcher cycle.
2. Confirm discovery, session reconciliation, child arguments, logs, session heartbeats, runtime fetches, review comments, comment replies, checks, and `reviewed_revisions` are correct.
3. Before opening shipping, confirm the current revision is reviewed and no unanswered comments or blockers remain.
4. Confirm no stale `running` sessions remain in SQLite.
5. Set `arc_review_watch.allow_ship: true` explicitly:

```yaml
arc_review_watch:
  allow_ship: true
```

6. Run one more `--once --dry-run` to confirm config parsing and event metadata.
7. Start the live watcher and monitor TUI plus `runner-logs/arc-review-watch.events.jsonl`.

If an unexpected ship attempt is suspected, immediately stop the watcher, set `allow_ship: false`, remove only stale locks/processes, and inspect the SQLite sessions plus event logs before restarting.
