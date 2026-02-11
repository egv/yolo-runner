---
id: yr-oo5l
status: closed
deps: [yr-3izt]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Fix ACP shutdown after prompt completion

Fix the ACP client lifecycle so yolo-agent never hangs waiting for the ACP transport to close.

Key requirements:
- After verification completes, proactively shut down the ACP connection.
- Close stdin/stdout handles so the JSON-RPC read loop can unwind.
- Do not block indefinitely waiting for connection.Start() to return.

Add/adjust tests as needed to prevent regressions.

## Acceptance Criteria

Given opencode reaches session.idle but keeps the process alive,
when the client completes verification,
then the runner returns a result without hanging.

Given the regression test for stdout-open,
when running go test,
then it passes.


## Notes

**2026-02-11T13:39:18Z**

Completed: ACP shutdown now closes connection/stdin/stdout with bounded wait to avoid idle transport hangs.
