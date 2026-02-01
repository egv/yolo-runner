---
id: yolo-runner-127.6.13
status: open
deps: []
links: []
created: 2026-01-22T11:06:24.323685+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Heartbeat shows absurd last output age (e.g. 156728s)

## Problem
The heartbeat line sometimes shows an absurdly large `last output` age (e.g. `opencode running - last output 156728s`).

## Repro
- Run the Go runner with a task that has existing `runner-logs/opencode/<id>.jsonl` from a previous session.
- Observe `last output` age jumps to a huge number immediately.

## Expected
`last output` should reflect time since the most recent JSONL output for *this* run, or be clamped to a reasonable range if the log is stale.

## Suspected area
`internal/ui/progress.go` initializes `lastOutput` from file mtime, which may be very old if the log existed from a prior run. It never resets on run start.

## Acceptance
- On run start, `last output` uses the current time or resets if log mtime is older than the run start time
- No huge stale ages displayed
- `go test ./...` passes with a regression test


## Notes

opencode stall category=permission runner_log=runner-logs/opencode/yolo-runner-127.6.13.jsonl opencode_log=/Users/egv/.local/share/opencode/log/2026-01-24T135216.log session=ses_40fb91c16ffe0NrhAVPKHEWyY7 last_output_age=10m2.403820585s opencode_tail_path=runner-logs/opencode/yolo-runner-127.6.13.jsonl.tail.txt


