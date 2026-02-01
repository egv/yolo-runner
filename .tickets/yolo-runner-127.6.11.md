---
id: yolo-runner-127.6.11
status: in_progress
deps: []
links: []
created: 2026-01-20T15:18:49.75778+03:00
type: bug
priority: 1
parent: yolo-runner-127.6
---
# Watchdog: bd update blocked can fail (task left in_progress)

## Problem
When OpenCode stalls, the watchdog attempts to mark the issue `blocked` with a long `--reason` string including `opencode_tail=...`.

Observed behavior:
- Runner prints the intended command:
  - `$ bd update <id> --status blocked --reason ... opencode_tail=...`
- The command fails with exit 1.
- The bead remains `in_progress`, leaving the queue in a bad state.

## Expected
- Stalled tasks should reliably transition to `blocked`.
- Even if the detailed reason update fails (argument too long / invalid characters / bd limits), the runner should fall back to a shorter reason and still set `blocked`.

## Suspected causes
- `--reason` length too large (includes huge `opencode_tail` with spaces and `|`)
- bd might reject certain characters or overall argument size.

## Acceptance
- Reproduce failure with a regression test or documented limit
- Implement a safe reason truncation/sanitization (and/or store tail in file and reference it)
- Ensure runner never leaves a task `in_progress` when watchdog triggers
- `go test ./...` passes



