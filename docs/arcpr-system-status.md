# Arc PR runner: system description and current operational status

**Status snapshot:** 2026-07-14. This is an evidence-backed incident snapshot, not a claim that the runner is healthy. Queue state changes continuously; use the commands in [Live inspection](#live-inspection) as the source of truth.

## What the system is intended to do

The Arc PR path has three separate responsibilities:

```text
Arcanum API
    │  open PRs where configured user is author or subscriber
    ▼
arcpr source
    │  enqueue `pr-review`, consume results, post replies, resolve threads
    ▼
SQLite queue (.yolo-runner/watch.db)
    │
    ▼
runner pool
    │  prepare Arc checkout, rebase author PRs, invoke Codex, commit/push/publish
    ▼
Arcanum PR branch and review threads
```

The source discovers PRs through both Arcanum queries:

- `subscriber(genaevstratov);open()` for PRs assigned to the reviewer;
- `author(genaevstratov);open()` for PRs authored by the configured author.

An authored PR runs in **author mode**. Before Codex reviews or implements a comment, the runner attempts to prepare the PR checkout and rebase it on `trunk`. A successful implementation must commit, push, publish the active Arcanum version, then enqueue `resolve-pr-comment`. The source consumes that follow-up by posting a reply such as `Fixed in <commit>` and resolving the root review thread.

## The supported production topology

`yolo-agent watch` is the normal supervisor. It starts both the configured source and the configured runner pool. It is the only command that automatically converts newly discovered PRs into running work.

```bash
env -u ARC_TOKEN ./bin/yolo-agent config validate --repo .
env -u ARC_TOKEN ./bin/yolo-agent watch \
  --repo . \
  --environments ~/.yolo-runner/environments.yaml \
  --events "runner-logs/watch-$(date +%Y%m%d_%H%M%S).events.jsonl" \
  --tui
```

`env -u ARC_TOKEN` is intentional for this installation: the Arcanum client falls back to `~/.arc/token`. Do not print that token or substitute an unrelated token from the environment.

### What not to use as a supervisor

`yolo-agent source arcpr` only polls Arcanum, writes queue items, and consumes completed results. It **does not execute queue items**. An item-specific command such as `yolo-agent runner --item-id <id>` only executes that one item and exits.

Running those two commands together is a focused debugging pattern, not a fleet supervisor. It caused a concrete failure on 2026-07-14: PR `14422811` was discovered and queued, but no general runner was available to claim it because the only worker was pinned to PR `14330209`.

If a split-process run is necessary, start a long-lived runner pool as well as the source:

```bash
env -u ARC_TOKEN ./bin/yolo-agent source arcpr \
  --repo . --profile arcpr --queue .yolo-runner/watch.db --stream

env -u ARC_TOKEN ./bin/yolo-agent runner \
  --queue .yolo-runner/watch.db \
  --environments ~/.yolo-runner/environments.yaml \
  --presets arcpr \
  --runner-id arcpr-pool-1 \
  --capacity 1
```

Use capacity `1` until per-PR workspace locking is proven: author-mode items for the same PR share a mount and must not rebase or push concurrently.

## Configuration currently in use

Repository configuration (`.yolo-runner/config.yaml`):

- reviewer: `genaevstratov`
- author: `genaevstratov`
- polling interval: `2m`
- `allow_ship: false`
- queue: `.yolo-runner/watch.db`
- Arc PR source/preset: `arcpr`

The local environment preset (`~/.yolo-runner/environments.yaml`) uses Codex with `gpt-5.6-sol`, a 45 minute runner timeout, a 10 minute watchdog timeout, and `max_concurrent: 2`. The YAML contains no reasoning-effort setting, so a `medium` effort selection is **not verifiable from runner configuration**.

## Snapshot: observed state on 2026-07-14

### Confirmed working behavior

- PR `14330209` active version `30` was verified published through the Arcanum API.
- Review comment `18941038` on PR `14330209` received a runner reply naming commit `9c6ec09765691f7bdc1c084ed53d37295bb9cba7` and was resolved.
- PR `14422811` was not filtered out: both the author and subscriber discovery queries returned it. It was queued as author-mode work for three unresolved comments.

### Queue snapshot

| PR | Observed queue state | Meaning |
| --- | --- | --- |
| `14330209` | 9 implementation items done; 8 resolve-comment items done; 1 implementation failed; 6 implementation items pending; older review items done/cancelled/pending | Some comments were applied and closed, but the PR is not complete. |
| `14422811` | author-mode review discovered and run; subsequent review/implementation items have failed | The PR was discovered, then checkout/mount handling failed. |

### Current failures requiring implementation work

1. **Stale or damaged Arc mount recovery — fixed 2026-07-14.** PR `14422811` failed when `arc mount` found a previous abnormal mount termination (`Device not configured`), then later when its object-store `.arc/port` was absent. A later push failed because the mount was no longer an Arc repository. `PreparePRCheckout` now classifies stale-state errors (dead FUSE mount, damaged store, "not a mounted arc repository"), force-unmounts, wipes the per-PR mount and object store, and retries the preparation once before declaring the task failed. Verified live against PR `14422811` (guarded test `TestPreparePRCheckoutLiveRecovery` in `internal/arcanum/pr_checkout_live_test.go`, run with `YOLO_LIVE_CHECKOUT_PR=<pr-id>`).
2. **Rebase conflicts are terminal instead of being resolved by the coding agent.** PR `14330209` failed during `arc rebase trunk` on `services/swarm-generator/internal/service/ya.make`. The task failed before the model could inspect and resolve the conflict. The intended rebase-first policy exists, but automatic conflict resolution does not.
3. **Manual source plus item-scoped workers is not a reliable run mode.** It leaves unrelated discovered PRs queued. Use `watch`, or a true long-lived runner pool, rather than manually pinning one worker to a single item.
4. **A worker killed after claiming an item leaves a lease until expiry.** The queue requeues it after the ten-minute lease and retry backoff, but it does not immediately reassign the task. An attached supervisor avoids this failure mode; detached ad-hoc process launching did not.

These are known blockers, not resolved features. A green queue item only means the recorded work completed; verify the Arcanum reply, issue status, and published active version for every comment.

## Live inspection

```bash
# Is a source and a pool worker actually running?
pgrep -fl yolo-agent

# Queue state for one PR.
sqlite3 -header -column .yolo-runner/watch.db \
  "select id, kind, state, attempt, claimed_by, not_before, updated_at
   from work_items where source_ref = 'pr:<PR_ID>' order by created_at;"

# Completed or failed runner result for one item.
sqlite3 -header -column .yolo-runner/watch.db \
  "select item_id, status, payload, started_at, finished_at, consumed_at
   from work_results where item_id = '<ITEM_ID>';"

# Direct Arcanum verification of a comment's reply and resolution.
token="$(arc token show)"
curl -fsSL -H "Authorization: OAuth $token" \
  "https://a.yandex-team.ru/api/v1/public/review-requests/<PR_ID>/comments" \
  | jq '.data[] | select((.id|tostring) == "<COMMENT_ID>" or (.reply_to_id|tostring) == "<COMMENT_ID>")'
```

Do not report a PR as handled merely because a source poll ran, an item was queued, or a model completed. The required completion evidence is:

1. active Arcanum version is published;
2. the requested change is committed and pushed;
3. the root review comment has a runner reply;
4. the root review comment's `issue_status` is `resolved`.

## Recovery policy

- Do not stop a live implementation worker merely to restart the supervisor.
- For a terminal mount preparation failure, repair the mount/object-store lifecycle before retrying. Retrying the same stale mount is not recovery.
- For a terminal rebase conflict, the runner needs an explicit conflict-resolution path; blindly retrying only repeats the failure.
- After a fix, commit and push the runner code/config before starting a new `yolo-agent` run. Fresh task checkouts synchronize from `origin/main`.

The wider runbook is [arc-review-watch.md](arc-review-watch.md); this document takes precedence for the current incident state and known limitations.
