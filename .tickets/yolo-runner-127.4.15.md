---
id: yolo-runner-127.4.15
status: open
deps: [yolo-runner-127.4.14]
links: []
created: 2026-01-26T12:38:22.999333+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Tool call status badges

Render tool call status with emoji + color prefix before message output.

## Acceptance Criteria

- Tool call events display emoji + colored status prefix
- Badge appears before message text
- Unknown status uses neutral badge
- Add tests for formatting

## Notes

Depends on message aggregation


