# yolo-runner v3: Queue-Centric Split — Runners and Source Adapters

Status: APPROVED 2026-06-12
Scope: local-first, single-host, multi-process, with an in-process supervisor for normal operator runs. No network bus (the NATS distributed mode was deliberately removed and is not coming back).

## Summary

SQLite **is** the broker. Source adapter runtimes write typed work items into a shared WAL-mode SQLite queue and consume typed results from it; runner runtimes claim items with leases, materialize a workspace from a named **environment preset**, run the model pipeline, and write a result row. Runners never know source semantics — no Startrek labels, no PR comment formats in runner code. The runtimes can be started as separate processes or supervised by `yolo-agent watch`; both modes use the same queue and event protocol. Every process appends JSONL events to its own file, and the watch supervisor can also stream directly into the existing `yolo-tui`.

Decisions locked with the owner:
1. Separate processes on one host, durable local queue (no bus).
2. **Everything model-invoking is a work item**: `implement`, `review`, `preflight`, `split`, `pr-review`, `finalize`.
3. **Environment presets** named per project; work items carry only the preset name.
4. One multi-command binary (`yolo-agent`) plus `yolo-tui`, as today.

## Process inventory

```
                                  ┌───────────────────────────────┐
                                  │  ~/.yolo-runner/queue.db      │
                                  │  (SQLite, WAL)                │
                                  │  work_items / work_results /  │
                                  │  item_deps / runners          │
                                  └───────┬───────────▲───────────┘
            submit items / consume results│           │ claim / heartbeat / result
        ┌─────────────────────────────────┤           ├──────────────────────────┐
        │                 │               │           │                          │
┌───────┴───────┐ ┌───────┴───────┐ ┌─────┴─────┐ ┌───┴───────────┐  ┌───────────┴───┐
│ yolo-agent    │ │ yolo-agent    │ │ yolo-agent│ │ yolo-agent    │  │ yolo-agent    │
│ source        │ │ source        │ │ source    │ │ runner        │  │ runner        │
│ --profile st  │ │ --profile gh  │ │ --profile │ │ --presets     │  │ --presets     │
│ (startrek)    │ │ (github)      │ │ arcreview │ │ adapta        │  │ yolo-runner   │
└───────┬───────┘ └───────┬───────┘ └─────┬─────┘ └───┬───────────┘  └───────┬───────┘
        │ writeback       │               │           │ workspaces           │
        ▼                 ▼               ▼           ▼                      ▼
   Startrek API      GitHub API      Arcanum API   arc mount + branches   git clones

  events: every process appends ~/.yolo-runner/events/{proc-id}.jsonl
  `yolo-agent events follow | yolo-tui --events-stdin`   (TUI unchanged)
  preferred supervisor: `yolo-agent watch --tui`
```

| Command | Owns | Replaces |
|---|---|---|
| `yolo-agent runner [--presets a,b] [--concurrency N]` | claim loop, preset materialization, per-kind execution, heartbeats, results | embedded `agent.Loop` worker pool, `arc-pr-review-runner` child processes |
| `yolo-agent source --profile <name> [--once]` | poll loop, result-consumer loop, source writebacks, source-local state DB | `tracker-watch`, `arc-review-watch` |
| `yolo-agent queue <ls\|submit\|retry\|cancel\|gc>` | operator CLI over queue.db | hand-editing scheduler-state.json |
| `yolo-agent watch` | supervisor: start configured sources, autoscale runner pools, stream/TUI events | manual source+runner+events orchestration |
| `yolo-agent events follow [--since]` | merge-tail all event files → NDJSON stdout | per-command `--events` plumbing |

Lifecycle: separate source and runner processes are peers around queue.db — a runner does not need sources running and vice versa (the decoupling test). `yolo-agent watch` supervises the same runtimes in one process for normal operations. Singleton flocks per source profile and runner id (`tracker_watch_lock_unix.go` pattern); heartbeat rows reusing the `arcreview/state` pattern; reaping of expired leases is opportunistic in every process's poll tick (`restartStaleArcReviewSessions` pattern — no mandatory daemon).

## Work item model

Package `internal/workqueue` (store modeled on `internal/arcreview/state/store.go`: `modernc.org/sqlite`, `PRAGMA journal_mode=WAL`, `busy_timeout`, additive column migrations) + `internal/workitem` (types).

```sql
CREATE TABLE work_items (
  id               TEXT PRIMARY KEY,            -- ULID
  kind             TEXT NOT NULL,               -- implement|review|preflight|split|pr-review|finalize
  source           TEXT NOT NULL,               -- source profile name, e.g. "st-adapta"
  source_ref       TEXT NOT NULL,               -- opaque to runner: "ADAPTABOT-12", "pr:13862355"
  idempotency_key  TEXT NOT NULL UNIQUE,
  preset           TEXT NOT NULL,
  priority         INTEGER NOT NULL DEFAULT 0,
  payload          TEXT NOT NULL,               -- JSON, kind-specific
  state            TEXT NOT NULL,               -- pending|claimed|running|done|failed|cancelled
  attempt          INTEGER NOT NULL DEFAULT 0,
  max_attempts     INTEGER NOT NULL DEFAULT 3,
  not_before       TEXT NOT NULL DEFAULT '',
  claimed_by       TEXT NOT NULL DEFAULT '',
  lease_expires_at TEXT NOT NULL DEFAULT '',
  heartbeat_at     TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE INDEX idx_items_claim  ON work_items(state, preset, priority DESC, created_at);
CREATE INDEX idx_items_source ON work_items(source, state);
CREATE INDEX idx_items_lease  ON work_items(state, lease_expires_at);

CREATE TABLE item_deps (
  item_id    TEXT NOT NULL REFERENCES work_items(id),
  depends_on TEXT NOT NULL REFERENCES work_items(id),
  PRIMARY KEY (item_id, depends_on)
);

CREATE TABLE work_results (
  item_id     TEXT PRIMARY KEY REFERENCES work_items(id),
  status      TEXT NOT NULL,                    -- completed|blocked|failed
  payload     TEXT NOT NULL,                    -- JSON, typed per kind
  log_path    TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL, finished_at TEXT NOT NULL,
  consumed_at TEXT NOT NULL DEFAULT '',
  consumed_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE runners (
  id TEXT PRIMARY KEY, pid INTEGER NOT NULL, presets TEXT NOT NULL,
  capacity INTEGER NOT NULL, started_at TEXT NOT NULL, heartbeat_at TEXT NOT NULL
);
```

### Kinds: payloads and results

| kind | payload | result payload | shaped from |
|---|---|---|---|
| `preflight` | task id/title/description, parent summary, queue root | `{verdict: ready\|needs_info\|reply, questions[], reply_text}` | `preflight.RunInput`/`Result` |
| `split` | task content, queue root, language hint | `{tasks: [{title, description, deps}], order}` | `splitter.RunInput`/`StrictOutput` |
| `implement` | task content, prompt, base branch, retry context, gates | `{status, reason, branch, commit_sha, pr_url, review_verdict, artifacts}` | `Loop.runTask` inputs/outputs |
| `review` | branch/diff ref, prompt (standalone) | `{verdict, reason}` | review stage |
| `pr-review` | pr id, revision, unanswered comment ids, ship flag | `{replies[], review_verdict, ship_ready, revision_reviewed}` | `arcreview.ReviewResult` |
| `finalize` | parent ref, child branches, PR title | `{pr_url}` | `internal/agent/finalizer.go` |

**Granularity decision**: an `implement` item executes the whole implement → review → land pipeline inside one work item (extracted intact from `Loop.runTask` into `internal/executor`). Review retries, merge-conflict remediation, and landing need workspace affinity; chaining them as separate queue items would force workspace handoff. `review` remains a standalone kind for source-requested reviews. "Everything is a work item" holds at *job* granularity, not per model call.

### Claim protocol

Single atomic statement (SQLite ≥3.35 `UPDATE … RETURNING`): select the oldest highest-priority `pending` item in a served preset whose `item_deps` are all `done` and `not_before` has passed; set `claimed`, lease, attempt+1. Heartbeat extends the lease every `watchdog_interval`; lease default 2× `watchdog_timeout`. Reaper: expired lease → `pending` with exponential `not_before` if attempts remain, else `failed` + synthesized result so the source always gets an answer.

Guarantees: **at-least-once execution**; **exactly-once admission** (`idempotency_key` unique); **idempotent writebacks** (existing `IdempotentSplitSubtaskCreationService`, split markers, `answered_comments`, `reviewed_revisions`). queue.db is derived state — deletable; sources rebuild from tracker truth.

## Environment presets

Package `internal/envpreset`; definitions in `~/.yolo-runner/environments.yaml` (global — runners are project-agnostic daemons and must not need to locate repos to resolve presets).

```yaml
presets:
  adapta:
    workspace: { strategy: arc-shared, mount: ~/arcadia, subpath: marvel/gena/adapta }
    landing:   { type: arc-pr, title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}" }
    agent:     { backend: codex, model: gpt-5.5, runner_timeout: 20m }
    limits:    { max_concurrent: 1 }            # shared checkout ⇒ serialized
    env:       { passthrough: [STARTREK_TOKEN] }
  yolo-runner:
    workspace: { strategy: git-clone, origin: ~/yolo-runner, base_branch: main }
    landing:   { type: git-merge }
    agent:     { backend: codex, model: gpt-5.5 }
```

- Items carry the preset **name**; the runner resolves at claim time (config drift is a feature — lets you hotfix model choice for queued items; the resolved preset hash is recorded in the result for audit). Secrets never enter queue.db.
- Materialization: `git-clone` → existing `agent.GitCloneManager`; `arc-shared` → shared mount + per-item branch via `vcs/arc`, serialized by a per-preset flock (landing-lock pattern); `path` → run in place (model-only kinds: preflight/split).
- Landing policy comes from the preset, not the source: `git-merge` (merge+push, landing lock), `arc-pr` (deferred PR, current behavior), `none` (report branch, source decides).
- Startrek queues may set `preset` per queue. This lets one Startrek source watch many queues while routing each queue's work to the correct workspace, landing policy, model, and concurrency limit.

## Source adapter contract

Packages `internal/sourcehost` (generic runtime) + `internal/sources/{startrek,arcpr,beads,github}` (thin adapters over existing clients).

```go
type Source interface {
    Name() string
    // Poll inspects the external system and returns submissions that should
    // exist. Idempotent — resubmission is safe (idempotency keys dedupe).
    Poll(ctx context.Context) ([]workqueue.Submission, error)
    // HandleResult writes a result back to the external system and may return
    // follow-up submissions (chains live here). Must be idempotent.
    HandleResult(ctx context.Context, item workitem.Item, res workitem.Result) ([]workqueue.Submission, error)
}
```

`sourcehost.Run` provides what `tracker_watch.go` hand-rolls: resilient poll loop (`runResilientWatchPollLoop` survives), result-consumer loop (unconsumed results for this source → `HandleResult` → mark consumed transactionally with follow-up enqueues), opportunistic reaper, flock, heartbeat, events. Each source keeps a private state DB `~/.yolo-runner/sources/<profile>.db` (the arcreview `answered_comments`/`reviewed_revisions` tables move here; startrek records subtask-ID → item-ID maps).

### Startrek chain (replaces the tracker-watch inline cycle)

```
Poll: ready ticket, no open item        → submit preflight (key: st/<id>/preflight/<rev>)
HandleResult(preflight):
  needs_info / reply → post comment + transition (existing needs-info flow); no follow-up
  ready + decomposable → submit split    ready + leaf → submit implement
HandleResult(split):  create subtasks (IdempotentSplitSubtaskCreationService) → submit
                      implement items with item_deps mirroring splitter order
HandleResult(implement): transition/labels/PR comment; all children done → submit finalize
```

All Startrek labels, transitions, and comment formats live **only** in `internal/sources/startrek`.

### arcreview mapping

`Poll`: existing discovery (`arcanum` list/filter + reviewed-revision/answered-comment checks) → one `pr-review` item per (PR, revision, comment-set). `HandleResult`: appliers + ship gate, record state. The `pr_sessions` child-process machinery is fully subsumed by queue leases — it was a bespoke queue and graduates into the general one.

## Events & observability

`contracts.Event` gains optional `proc` and `item_id` fields (omitempty — TUI byte-compatible). Each process appends to its own `~/.yolo-runner/events/<proc>.jsonl` (single writer per file). `yolo-agent events follow` merge-tails by timestamp into NDJSON for `yolo-tui --events-stdin`.

## Failure model

| Failure | Behavior |
|---|---|
| Runner crash mid-item | lease expires → requeue (attempt++) or fail with synthesized result; fresh workspace per attempt; landing guarded by lock + idempotent merge check |
| Source crash | poll resumes from external truth; unconsumed results re-handled on restart |
| Duplicate submission | unique idempotency_key swallows it |
| Duplicate result handling | HandleResult idempotent; consumed_at set transactionally with follow-up enqueues |
| queue.db corruption | WAL + `VACUUM INTO` backup via `queue gc`; worst case: delete — queue is derived state |
| Concurrent claims | impossible — single-statement atomic claim |

## Survives / dies

**Survives**: `contracts` Event/EventSink/AgentRunner/RunnerRequest/RunnerResult/VCS; `codingagents` catalog + codex/claude/opencode/kimi adapters; `vcs/git` + `vcs/arc` + `CloneManager` + landing lock (→ `internal/envpreset`); `preflight`/`splitter` prompt builders + parsers (→ payload schemas); `arcreview` appliers/gate/prompts; all tracker clients (source-side only); `yolo-tui` + `internal/ui` unchanged.

**Dies**: `tracker_watch.go` inline orchestration (→ sources/startrek + sourcehost); `arc_review_watch.go`/`arc_review_process.go` session+child-process machinery (→ queue leases); `internal/agent/loop.go` scheduling half + `scheduler_state.go` (→ queue state); `finalizer.go` as loop hook (→ `finalize` kind); `TaskManager`/`TaskEngine` coupling (graph gating → `item_deps`).

## Risks (flagged)

1. **Implement-as-one-item**: coarse retry granularity; landing failure retries the whole item. Mitigation: pipeline checkpoints branch/sha into heartbeat metadata for fast-forward retries. Trapdoor: if standalone reviews ever need the implementer's workspace, revisit.
2. **Multi-process SQLite contention**: modernc driver `SQLITE_BUSY` under concurrent `BEGIN IMMEDIATE` needs hard testing first (the keystone task). Fallback that preserves the schema: a single queue-owner process behind a unix socket.
3. **Preset resolution at claim time**: config drift between submit and run is accepted (audit via preset hash in results).
4. **Arc shared mount**: corruption risk under parallel items — enforced `max_concurrent: 1` per shared-workspace preset via the claim filter.

## Migration

Six dependency clusters (A: workitem types, B: workqueue store, C: envpreset, D: executor extraction from `Loop.runTask`, E: runner daemon, F: startrek source, G: arcpr+beads sources, H: legacy deletion), implemented as ONE beads epic with task-level dependency links; `tracker-watch`/`arc-review-watch` remain as rollback paths until their source replacements have run the production workflows (adapta Startrek dailies, beads epic runs) for several days. Behavior-parity gate for the executor extraction: JSONL event-stream diff of a real run before/after.
