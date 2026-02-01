---
id: yolo-runner-127.6.16
status: closed
deps: []
links: []
created: 2026-01-24T16:37:37.248534+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Runner error should explicitly say to run yolo-runner init

When the runner refuses to start because the YOLO agent is missing permission, it says: "yolo agent missing permission: allow; run opencode init". That is unclear for yolo-runner users. The error should explicitly instruct: "run yolo-runner init" (or "./yolo-runner init") and mention the agent file location (.opencode/agent/yolo.md).

Repro:
- Run ./yolo-runner --root yolo-runner-127.6 in a repo without .opencode/agent/yolo.md
- See the error above

Expected:
- Error explicitly calls out yolo-runner init and the agent file path.


