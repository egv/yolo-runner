---
id: yolo-runner-r5w.13
status: closed
deps: [yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:49:22.384868+03:00
type: task
priority: 1
parent: yolo-runner-r5w.15
---
# v1: OpenCode CLI adapter (with --model)

Implement the OpenCode adapter for the Go runner. This must be Go code.

Files:
- Create: internal/opencode/client.go
- Create: internal/opencode/client_test.go

Rules:
- Hardcode agent name `yolo`
- Do not add any new Python files

Acceptance:
- When model is empty, command args do not include --model
- When model is set, args include --model <id>
- Env includes CI=true and OPENCODE_DISABLE_* flags
- Writes stdout to runner-logs/opencode/<issue>.jsonl (overwrite)
- go test ./... passes

## Acceptance Criteria

- If model is empty, command args do not include --model
- If model is set, command args include --model <id>
- Run sets env vars: CI=true and OPENCODE_DISABLE_* flags matching Python runner
- Run ensures config dir exists and opencode.json exists
- Run writes stdout to the provided log path (overwrite)
- Unit tests validate args and env construction


