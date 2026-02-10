---
id: yr-0tbv
status: open
deps: [yr-fx2p]
links: []
created: 2026-02-10T00:12:27Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T10 Error taxonomy and user-facing remediation for Linear sessions

STRICT TDD: failing tests first. Map webhook/auth/GraphQL/runtime failures to meaningful activity/error messages and stderr logs.

## Acceptance Criteria

Given common failure modes, when errors occur, then both Linear activity and local stderr include specific remediation.

