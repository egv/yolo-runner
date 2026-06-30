# F5b Dogfood Re-run Verification

Task: `yolo-yolo-board-fleet-tui-bypq.35`

## Goal

Verify that a dogfood run which previously wedged on a permission denial emits a structured `agent_blocked` event with `reason=permission_denied`, and that the event is visible in `yolo-tui` along with fleet events.

## Script

Run from the repository root:

```bash
make build
scripts/verify-f5b-dogfood-rerun.sh --run --duration 90
```

For local dogfood verification without Startrek/Arc tokens, use the isolated fixture mode. It still runs `yolo-agent watch`, a queue runner, the Claude permission-denied detector, and `yolo-tui` against a temporary repo/queue:

```bash
make build
scripts/verify-f5b-dogfood-rerun.sh --run --fixture --duration 20
```

The verifier writes and checks:

- `/tmp/yolo-board.events.jsonl`
- `/tmp/yolo-board.stream.jsonl`
- `/tmp/yolo-board.watch.stderr.log`
- `/tmp/yolo-board.tui.txt`

It fails unless all required evidence is present:

- `agent_blocked`
- `reason=permission_denied`
- `source_poll`
- `runner_alive`
- `agent_blocked` and `permission_denied` rendered by `yolo-tui`

## 2026-06-30 Attempt

Red check:

```bash
test -f /tmp/yolo-board.events.jsonl \
  && rg '"type"[[:space:]]*:[[:space:]]*"agent_blocked"' /tmp/yolo-board.events.jsonl \
  && rg '"reason"[[:space:]]*:[[:space:]]*"permission_denied"' /tmp/yolo-board.events.jsonl \
  && rg '"type"[[:space:]]*:[[:space:]]*"source_poll"' /tmp/yolo-board.events.jsonl \
  && rg '"type"[[:space:]]*:[[:space:]]*"runner_alive"' /tmp/yolo-board.events.jsonl
```

Result: failed before implementation because the required event evidence was absent.

Targeted tests:

```bash
go test ./cmd/yolo-tui ./cmd/yolo-agent -run 'TestRunMainRendersAgentBlockedReasonAndDetail|TestWatchCommand|TestRunMain' -count=1
```

Result: passed.

Dogfood run:

```bash
make build
scripts/verify-f5b-dogfood-rerun.sh --run --duration 15
```

Result: failed. `yolo-agent watch` validated repository config, emitted `runner_alive` and `source_poll`, then exited before producing the required permission-denied block.

Observed blocker:

```text
start watch runner pool "startrek-adapta-implementers" replica 0: environment preset "startrek-adapta" is not defined in /Users/genaevstratov/.yolo-runner/environments.yaml
```

Current marker state in `/tmp/yolo-board.events.jsonl`:

- `runner_alive`: present
- `source_poll`: present
- `agent_blocked`: absent
- `permission_denied`: absent

Conclusion: the verification harness is in place and fails closed, but the F5b acceptance criteria were not satisfied in this local run because the configured dogfood environment was incomplete.

## 2026-06-30 Remediation Run

Red check:

```bash
scripts/verify-f5b-dogfood-rerun.sh
```

Result: failed before implementation with `missing agent_blocked event in /tmp/yolo-board.events.jsonl`.

Fix:

- Wired the queue-runner implement resolver to preserve the runner event sink, so executor progress events from the Claude adapter are emitted into the watch stream.
- Added `--fixture` to `scripts/verify-f5b-dogfood-rerun.sh` to build an isolated local dogfood repo/queue and drive the real `yolo-agent watch` path through a Claude permission-denied stream-json fixture.

Verification command:

```bash
make build
scripts/verify-f5b-dogfood-rerun.sh --run --fixture --duration 20
```

Result: passed.

Evidence in `/tmp/yolo-board.events.jsonl`:

- `source_poll`: present at `2026-06-30T12:20:07Z`
- `runner_alive`: present at `2026-06-30T12:20:07Z`
- `agent_blocked`: present for `F5B-DOGFOOD`
- `reason=permission_denied`: present on the `agent_blocked` event

Representative event:

```json
{"type":"agent_blocked","task_id":"F5B-DOGFOOD","message":"Claude requested permissions for Bash, but you haven't granted them.","reason":"permission_denied"}
```

TUI evidence:

```bash
rg 'agent_blocked|permission_denied' /tmp/yolo-board.tui.txt
```

Result: passed via the verifier; `/tmp/yolo-board.tui.txt` renders both `agent_blocked` and `permission_denied`.

Tests:

```bash
go test ./cmd/yolo-agent -run TestRunnerImplementResolverForPresetsPreservesEventSink -count=1
go test ./cmd/yolo-tui ./cmd/yolo-agent -run 'TestRunMainRendersAgentBlockedReasonAndDetail|TestWatchCommand|TestRunMain|TestRunnerImplementResolverForPresetsPreservesEventSink' -count=1
go test ./internal/claude -run TestStdinTaskSessionExecuteEmitsPermissionDeniedAgentBlocked -count=1
go test ./cmd/yolo-agent ./cmd/yolo-tui ./internal/claude ./internal/executor -count=1
```

Result: all passed.
