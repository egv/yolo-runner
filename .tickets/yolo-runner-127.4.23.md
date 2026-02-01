---
id: yolo-runner-127.4.23
status: blocked
deps: []
links: []
created: 2026-01-27T12:32:08.19772+03:00
type: task
priority: 0
parent: yolo-runner-127.4
---
# v1.3: Honor --model in ACP mode

Wire --model flag so ACP runs use the requested model (override agent config or pass via ACP if supported).

## Acceptance Criteria

- --model flag changes model used in ACP runs
- Works for empty model (defaults to agent config)
- Add tests covering model override behavior
- go test ./... passes


