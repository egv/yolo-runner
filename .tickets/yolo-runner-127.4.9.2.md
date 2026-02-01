---
id: yolo-runner-127.4.9.2
status: closed
deps: []
links: []
created: 2026-01-19T18:40:25.710716+03:00
type: task
priority: 0
parent: yolo-runner-127.4.9
---
# Align: Keep OpenCode JSONL log clean (stderr separate)

Ensure runner-logs/opencode/<issue>.jsonl remains valid JSONL by not mixing stderr into the file.

Why:
- Python runner writes only stdout to the JSONL file.
- Go runner currently redirects BOTH stdout and stderr into the same file, which can insert non-JSON lines and corrupt downstream parsing/watchdog logic.

What:
- Change cmd/yolo-runner/exec.go startCommandWithEnv:
  - stdout -> JSONL file (same as today)
  - stderr -> either os.Stderr OR a separate file (recommended: <issue>.stderr.log)
- Update internal/opencode/watchdog.go classification to use OpenCode service logs for diagnosis, not the mixed JSONL file.

Files:
- Modify: cmd/yolo-runner/exec.go
- Modify (if needed): internal/opencode/client.go
- Add/Modify tests:
  - internal/opencode/client_test.go (assert log file contains only what the runner writes)

Acceptance:
- Given opencode writes to stderr, JSONL file is not polluted (stderr not present)
- Given runner executes opencode, JSONL file still exists and has stdout content
- go test ./... passes

## Acceptance Criteria

- stderr is not written into JSONL file
- JSONL file still captures stdout
- Tests cover the behavior
- go test ./... passes


