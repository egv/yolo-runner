---
id: yolo-runner-127.3.6
status: open
deps: []
links: []
created: 2026-02-01T15:56:19.425701064Z
type: bug
priority: 2
parent: yolo-runner-127.3
---
# Remove direct tool call echoing, add to log bubble instead

Tool call logs are being printed directly to the console and are not being added to the log list that is displayed as a bubble. We should remove the direct echoing of tool calling messages to the console and instead add them to the logging bubble.


