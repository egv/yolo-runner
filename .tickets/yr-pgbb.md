---
id: yr-pgbb
status: open
deps: [yr-fx2p]
links: []
created: 2026-02-10T00:12:26Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T7 Manage AgentSession externalUrls

STRICT TDD: failing tests first. Set/update externalUrls to runner session/log links and preserve uniqueness semantics.

## Acceptance Criteria

Given active session, when execution updates occur, then externalUrls are visible and valid for current run context.

