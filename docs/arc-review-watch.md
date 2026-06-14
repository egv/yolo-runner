# Arc Review Watch Deprecation Runbook

`yolo-agent arc-review-watch` is a compatibility shim. It prints a deprecation notice and delegates to `yolo-agent source arcpr` with the same `--repo`, `--profile`, `--once`, `--events`, and `--stream` values.

Use `source arcpr` for new runs:

```bash
./bin/yolo-agent source arcpr --repo . --profile arc-review --queue .yolo-runner/queue.db --once
```

The legacy `--dry-run` flag is accepted by `arc-review-watch` for command compatibility, but the shim warns that it is ignored. Use a disposable queue path for validation runs that must not affect production queue state.

## PR Review Model

The Arc PR source is cross-project. It discovers incoming reviews with `arc pr list -i` (`arc pr list --json -i --status open`) from the current Arc/Arcanum identity instead of scanning a configured workspace. The source records the PR id, head revision, unanswered comments, and `allow_ship` flag in a queue item for the profile's preset.

Each PR review work item receives an isolated checkout. The runner does not reuse the preset workspace for PR code; it prepares a per-PR Arc mount under `mounts_base_dir` backed by `objects_base_dir`, then checks out the PR with `arc pr checkout <pr-id> --detached --force`. The checkout is cleaned up after the review attempt.

The preset still selects runner capacity, backend, model, timeout, and environment allow-list. The PR checkout supplies the files being reviewed, and the review cycle auto-detects the project root from changed files by finding the nearest `ya.make`; that project root determines the validation command and local `AGENTS.md`/`CLAUDE.md` context used in the prompt.

PR review does not configure or require MCP servers. Keep MCP setup in the coding-agent configuration only if that backend needs it for other modes; the PR-review path itself uses the existing Arcanum clients, review prompts, reply/review appliers, and ship gate.

## Configuration

The Arc PR source still reads the top-level `arc_review_watch` block from `.yolo-runner/config.yaml`.

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
  state_path: .yolo-runner/arcpr-source-state.db
  reviewer: alice
  allow_ship: false
  objects_base_dir: ~/.yolo-runner/pr-objects
  mounts_base_dir: ~/.yolo-runner/pr-mounts
```

Important fields:

- `poll_interval`: time between source polling iterations when `--once` is omitted.
- `state_path`: SQLite source state for `reviewed_revisions` and `answered_comments`.
- `reviewer`: required reviewer identity used by PR discovery. Omitted or blank discovers no eligible PRs.
- `allow_ship`: copied into submitted PR review work items. `allow_ship` defaults to `false`; omitted or `false` keeps shipping disabled.
- `objects_base_dir`: base directory for per-PR arc object stores. Omit it to use `~/.yolo-runner/pr-objects`.
- `mounts_base_dir`: base directory for per-PR arc mount checkouts. Omit it to use `~/.yolo-runner/pr-mounts`.

Validate the file before a live run:

```bash
./bin/yolo-agent config validate --repo .
```

## Running

Prefer the source command:

```bash
./bin/yolo-agent source arcpr --repo . --profile arc-review --queue .yolo-runner/queue.db --events "runner-logs/source-arcpr-$(date +%Y%m%d_%H%M%S).events.jsonl" --stream | ./bin/yolo-tui --events-stdin
```

The deprecated shim is equivalent except that it has no `--queue` flag, so the workqueue default path is used:

```bash
./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once
```

Run queue workers separately:

```bash
./bin/yolo-agent runner --queue .yolo-runner/queue.db --presets arc-review
```

## State Inspection And Reset

Inspect reviewed revisions:

```bash
STATE=.yolo-runner/arcpr-source-state.db
sqlite3 "$STATE" "select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;"
```

Inspect answered comments:

```bash
sqlite3 "$STATE" "select pr_id, comment_id, answered_at from answered_comments order by answered_at desc;"
```

Reset one PR when the source should submit a fresh review item for the current revision or comments:

```bash
sqlite3 "$STATE" "delete from reviewed_revisions where pr_id = '<pr-id>';"
sqlite3 "$STATE" "delete from answered_comments where pr_id = '<pr-id>';"
```

If the source state database is corrupt or points at the wrong repo, back it up and let the next source run recreate the source-owned schema:

```bash
mv "$STATE" "$STATE.$(date +%Y%m%d_%H%M%S).bak"
```

## Operational Notes

Queue leases replace the old PR session table and child review process lifecycle. There is no `pr_sessions` table, no stale-session restart, and no `arc-pr-review-runner` command. Stale work is requeued through the workqueue lease reaper.

Shipping is controlled by `arc_review_watch.allow_ship` at source submission time and by the PR review runner payload. The ship gate still requires a reviewed current revision, no open blockers, no unanswered comments, non-failing checks, and a model verdict of `ship`.
