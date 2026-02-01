---
id: yolo-runner-127.4.14
status: blocked
deps: []
links: []
created: 2026-01-26T12:38:22.917096+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Aggregate agent message chunks

Collect ACP message chunks (agent_message, agent_thought, user_message) into full lines before rendering. Current output prints each token chunk separately (see agent_thought stream lines).

## Acceptance Criteria

- Buffer ACP message/thought chunks and emit a single line per completed message
- Newline (\n) is the delimiter for flushing a message
- Applies to agent_message, agent_thought, and user_message chunk streams
- No per-token spam in console/TUI output
- Add aggregation tests covering multi-chunk + newline flush
- go test ./... passes

## Notes

Probable cause: formatSessionUpdate logs each *chunk* via formatMessage; no aggregation/buffering. ACP streams token chunks, so we need per-session chunk buffer + flush on newline or end-of-message.


