---
id: yolo-runner-r5w.9
status: closed
deps: [yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:46:46.982388+03:00
type: task
priority: 2
parent: yolo-runner-r5w.17
---
# v1: Logging parity (runner + per-task)

Implement runner logging helpers in Go. This must be Go code.

Files:
- Create: internal/logging/jsonl.go
- Create: internal/logging/jsonl_test.go

Rules:
- Do not add any new Python files

Acceptance:
- Append to runner-logs/beads_yolo_runner.jsonl with fields timestamp, issue_id, title, status, commit_sha
- Timestamp is UTC in format 2006-01-02T15:04:05Z
- commit_sha defaults to HEAD
- go test ./... passes

## Acceptance Criteria

- Given a completed run, when logging, then runner summary appends an entry with timestamp, issue_id, title, status, commit_sha
- Given blocked run, when logging, then summary entry uses status=blocked and includes commit_sha from HEAD
- Given per-task log path, when running opencode, then stdout goes to `runner-logs/opencode/<issue>.jsonl`
- Given unit tests, when run, then they validate JSONL formatting and file creation


