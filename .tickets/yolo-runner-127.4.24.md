---
id: yolo-runner-127.4.24
status: closed
deps: []
links: []
created: 2026-01-27T12:32:08.27452+03:00
type: task
priority: 0
parent: yolo-runner-127.4
---
# v1.3: Show active model in output

Display the model actually used by OpenCode in the status output (from ACP events or stderr logs).

## Acceptance Criteria

- Output shows model in status line
- Model matches OpenCode runtime value
- Add test or fixture for formatting
- go test ./... passes


