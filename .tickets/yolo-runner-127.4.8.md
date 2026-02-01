---
id: yolo-runner-127.4.8
status: closed
deps: []
links: []
created: 2026-01-19T16:18:40.775675+03:00
type: task
priority: 0
parent: yolo-runner-127.4
---
# v1.2: Diagnose and fail-fast on OpenCode stalls (question/permission/deadlock)

Fix the Go runner so it never hangs indefinitely while waiting for OpenCode.

Part A: Diagnose why OpenCode stalls
- Determine the primary stall mechanism on this machine:
  - permission/doom_loop prompt (service=permission asking)
  - question prompt (service=question asking)
  - provider/network hang with no output
- Capture the relevant evidence in runner logs and/or an error message:
  - last N lines from ~/.local/share/opencode/log/<latest>.log (or equivalent)
  - opencode session id (if available)
  - last output timestamp/age for runner-logs/opencode/<issue>.jsonl

Part B: Fail fast + recoverable state
- Add a watchdog around the OpenCode subprocess:
  - Track output activity by observing runner-logs/opencode/<issue>.jsonl growth/mtime
  - If no growth for N seconds (configurable, default 600s), kill the OpenCode process group
  - Update the bead status to blocked with a short reason that includes the detected stall category and key evidence pointers
  - Exit non-zero so the runner does not silently succeed

Files:
- Modify: internal/opencode/client.go
- Modify: internal/runner/runner.go
- Create: internal/opencode/watchdog.go
- Create: internal/opencode/watchdog_test.go

Rules:
- Go only

Acceptance:
- Given OpenCode output does not change for >N seconds, runner terminates OpenCode and exits with an error
- Given the stall looks like a permission/question prompt, runner error message includes that classification and points to the relevant opencode log file
- Given the stall looks like no provider output, runner classifies as "no_output" and includes last-output age
- Bead is updated to status=blocked with the same classification info
- go test ./... passes

## Acceptance Criteria

- Watchdog kills OpenCode on no-output timeout
- Error includes classification and evidence pointers
- Bead is marked blocked with classification
- go test ./... passes


