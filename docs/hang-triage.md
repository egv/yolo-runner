# Yolo-Agent Hang Triage Runbook

Use this runbook when `yolo-agent` appears stuck (for example: a task emits `runner_started` but no `runner_finished`).

## 1) Reproduce with bounded risk

Run with a timeout so triage does not hang forever:

```bash
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --runner-timeout 20m
```

For live telemetry in TUI, use stream mode:

```bash
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --stream --runner-timeout 20m | ./bin/yolo-tui --events-stdin
```

If needed for full output detail, add `--verbose-stream`.

## 2) Identify where it is stuck

Start with `runner-logs/agent.events.jsonl`.

- If no `runner_started` is emitted for a task, the issue is likely in task selection/scheduler/tracker/VCS/clone setup.
- If `runner_started` appears but no `runner_finished`, the issue is in runner/opencode/ACP lifecycle.

## 3) Inspect runner + opencode logs

Primary paths:

- `runner-logs/agent.events.jsonl`
- `runner-logs/opencode/<task-id>.jsonl`
- `runner-logs/opencode/<task-id>.stderr.log`

If per-task clones are enabled, inspect the same paths under:

- `.yolo-runner/clones/<task-id>/runner-logs/agent.events.jsonl`
- `.yolo-runner/clones/<task-id>/runner-logs/opencode/<task-id>.jsonl`
- `.yolo-runner/clones/<task-id>/runner-logs/opencode/<task-id>.stderr.log`

## 4) Grep for known hang markers

Look for prompt-loop completion vs transport liveness:

```bash
rg -n "session.prompt .* exiting loop|session.idle publishing" runner-logs/opencode/<task-id>.stderr.log
```

Look for prompt/input blockers:

```bash
rg -n "service=permission|permission=question|service=question|request permission" runner-logs/opencode/<task-id>.stderr.log runner-logs/opencode/<task-id>.jsonl
```

## 5) Classify the hang category

Use one of these categories:

- `idle-transport-open`: prompt loop exits (`session.idle`) but ACP transport/process stays open.
- `permission-question`: permission or question prompts keep the run from progressing.
- `no-output-stall`: no meaningful ACP updates for longer than watchdog/timeout threshold.
- `other`: anything outside the above patterns.

## 6) Immediate next actions checklist

1. Ensure a non-zero timeout for triage (`--runner-timeout 20m` in local runs).
2. Inspect the latest `runner_finished`/`runner_warning` metadata in event stream.
3. Capture the last 100-200 lines from task stderr and ACP JSONL log.
4. Record the stall category and whether watchdog classification appeared (`opencode stall category=...`).
5. Open a follow-up ticket with log paths and category if unresolved.
