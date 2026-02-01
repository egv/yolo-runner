---
id: yolo-runner-127.2.3
status: closed
deps: []
links: []
created: 2026-01-19T11:20:28.353994+03:00
type: task
priority: 1
parent: yolo-runner-127.2
---
# v1.2: OpenCode heartbeat while logs grow

While OpenCode runs, print a compact heartbeat/spinner that proves the process is alive.

Behavior:
- Keep a "current state" string (e.g. "opencode running") and print it alongside the spinner.
- Spinner should advance (beat) when new bytes are appended to runner-logs/opencode/<issue>.jsonl.
- Also print last-output age (seconds) so stalls are obvious.
- Must not spam: render on the SAME terminal line using carriage return (CR, ). Only print a newline when:
  - the state changes, or
  - OpenCode finishes, or
  - an error occurs.

Files:
- Create: internal/ui/progress.go (or similar)
- Modify: internal/opencode/client.go (or call-site in internal/runner/runner.go)

Rules:
- Go only

Acceptance:
- Given OpenCode runs for >5s, stdout shows spinner output with current state
- Spinner advances on new output bytes
- Output is updated in-place (CR) rather than printing new lines repeatedly
- Shows last-output age seconds to detect hangs
- On completion, prints a final newline and "OpenCode finished" line
- go test ./... passes

## Acceptance Criteria

- Spinner prints during opencode execution with current state
- Spinner advances on new output bytes
- Uses CR to update in-place (no spam)
- Shows last-output age seconds to detect hangs
- Stops on completion and prints newline
- go test ./... passes


