# Task: Debug why the codex backend produces empty output for Arc PR reviews

## Context

`yolo-runner` is a Go task runner that polls Startrek queues and Arc PRs, then executes coding-agent work (reviews, implementations) via pluggable backends (claude, codex, opencode, etc.). The arcpr source was recently split into two modes — **reviewer mode** (review others' PRs) and **author mode** (triage comments on your own PRs). Both modes build a prompt, hand it to a backend adapter via `contracts.AgentRunner.Run()`, and parse the model's text output as JSON.

The feature works end-to-end with the **claude** backend (13 real reviews posted to Arcanum, substantive technical content). But switching to the **codex** backend produces empty model output, causing every review to fail with `"review result payload is required"` (the output buffer is empty when `ParseReviewResult` runs).

## The specific failure

When the arcpr preset uses `backend: codex, model: gpt-5.3-codex`:
- The runner claims a `pr-review` work item from the queue
- It prepares a PR checkout (`arc pr checkout --detached --force` into a per-PR mount)
- It builds the review prompt (metadata + changed file list + comments + checks + JSON contract)
- It calls `runner.Run(ctx, request)` where runner is a `codex.CLIRunnerAdapter`
- The codex adapter runs `codex app-server` (JSON-RPC protocol over stdin/stdout)
- **The adapter returns no model output** — the `output` bytes.Buffer in `runArcPRReviewModel` (cmd/yolo-agent/arc_pr_review_model.go) is empty
- `ParseReviewResult([]byte(""))` fails with "review result payload is required"

## What to investigate

1. **`internal/codex/runner_adapter.go`** — the `CLIRunnerAdapter.Run()` method. It has two modes: `runLegacyLineMode` and `runAppServerMode`. The catalog entry for codex (`internal/codingagents/builtin/codex.yaml`) sets `adapter: codex-app-server`, so `runAppServerMode` is the active path. Read this function fully and understand how it captures model output — the `AppServerCompletion` struct and `mergeAppServerStreamCompletion`.

2. **`internal/codex/task_session_runtime.go`** — the `AppServerTaskSession` and its JSON-RPC protocol. The `handleExecuteMessage` method processes responses. Check whether review-mode prompts (`contracts.RunnerModeReview`) are handled differently, and whether the `thread/start` → `thread/run` → response cycle actually delivers assistant message content back to the caller.

3. **`cmd/yolo-agent/arc_pr_review_model.go`** — the `runArcPRReviewModel` function. It captures model output via `OnProgress` callbacks that listen for `agent_text` progress events. Check whether the codex adapter emits `agent_text` progress events at all (the claude adapter does; codex may emit a different event type or none).

4. **The output capture path**: `runArcPRReviewModel` builds a `contracts.RunnerRequest` with an `OnProgress` callback that appends `agent_text` messages to a buffer. If codex doesn't emit `agent_text`-typed progress events (it might emit `command_run`, `tool_invoked`, or a different message type), the buffer stays empty even if the model produced output. This is the most likely root cause.

5. **`internal/contracts/` progress normalization**: check `NormalizeTaskSessionEvent` and `NewRunnerOutputProgress` — how do raw codex JSON-RPC responses map to `contracts.RunnerProgress` types? Does codex's assistant message content get classified as `agent_text`?

## How to reproduce

```bash
# Auth: unset ARC_TOKEN so it falls back to ~/.arc/token (the arc CLI's own token)
env -u ARC_TOKEN

# Discover PRs into a fresh queue
mv .yolo-runner/watch.db .yolo-runner/watch.db.bak
./bin/yolo-agent source arcpr --repo . --profile arcpr --queue .yolo-runner/watch.db --once --stream

# Run one review item with raw output dump
mkdir -p /tmp/yolo-prreview-raw
YOLO_PRREVIEW_RAW_DUMP_DIR=/tmp/yolo-prreview-raw ./bin/yolo-agent runner \
  --queue .yolo-runner/watch.db \
  --environments ~/.yolo-runner/environments.yaml \
  --presets arcpr --once

# Check if raw output was produced
cat /tmp/yolo-prreview-raw/*.out

# Check the runner event log for what the adapter emitted
cat ~/.yolo-runner/events/genaevstratov-OSX-arcpr.jsonl | python3 -c "
import sys, json
for line in sys.stdin:
    e = json.loads(line.strip())
    if e.get('type') not in ('agent_heartbeat', 'runner_alive'):
        print(e.get('type'), e.get('message','')[:120])
"
```

## Environment

- `codex` binary: `/opt/homebrew/bin/codex` (codex-cli 0.142.5)
- `arc` binary: `/opt/homebrew/bin/arc` (arcadia VCS CLI)
- `~/.arc/token` exists (arc CLI's OAuth token — do NOT set `ARC_TOKEN` env to a Tracker token, they're different)
- Arcadia mounts exist at `~/dev/arcadia/{adapta,lumi,swarm,...}` with stores at `~/.arc_stores/`
- Config: `.yolo-runner/config.yaml` (arcpr-only watch section is currently active)
- Presets: `~/.yolo-runner/environments.yaml` (arcpr preset uses `backend: codex, model: gpt-5.3-codex`)

## What "done" looks like

- A `pr-review` item claimed by the codex runner produces non-empty model output in the raw dump
- `ParseReviewResult` successfully parses the output into a `ReviewResult` with a summary, inline comments, and a ship verdict
- The review summary is posted to the Arc PR via the Arcanum API
- At least 3 reviews complete successfully (`state='done'` in the queue) when running `yolo-agent watch` with the codex backend

## Constraints

- The fix must be **backend-agnostic at the review-cycle layer** — don't add codex-specific logic to `arc_pr_review_model.go`, `review_prompt.go`, or the arcpr source. The fix belongs in the codex adapter (`internal/codex/`) or the contracts progress-normalization layer (`internal/contracts/`).
- The claude backend must continue to work — don't break `internal/claude/` or shared contract types.
- All existing tests must pass: `go test ./internal/codex/... ./internal/arcreview/... ./cmd/yolo-agent/...`
- Commit on `main` with a conventional commit message when done.

## Relevant files (in priority order)

```
internal/codex/runner_adapter.go          # CLIRunnerAdapter.Run, runAppServerMode, output capture
internal/codex/task_session_runtime.go     # AppServerTaskSession, JSON-RPC protocol, handleExecuteMessage
internal/contracts/runtime_session.go      # TaskSession interfaces, progress event types
internal/contracts/runner.go               # RunnerProgress, RunnerRequest, NormalizeTaskSessionEvent
cmd/yolo-agent/arc_pr_review_model.go      # runArcPRReviewModel — where output is captured via OnProgress
cmd/yolo-agent/runner_prreview.go          # runRunnerPRReview — wires the cycle together
internal/arcreview/review_result.go        # ParseReviewResult — where empty output fails
internal/codingagents/builtin/codex.yaml   # catalog entry: adapter=codex-app-server
~/.yolo-runner/environments.yaml           # arcpr preset: backend=codex, model=gpt-5.3-codex
```
