---
id: yr-nie0
status: closed
deps: [yr-py4x]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-4jk8
---
# E6-T4 Implement GitHub write operations

STRICT TDD: failing tests first. Implement status/data updates with triage metadata.

## Acceptance Criteria

Given blocked/failed outcomes, when writing updates, then issue state/comments are updated consistently.


## Notes

**2026-02-13T23:30:42Z**

Implemented GitHub write operations with strict TDD. Added failing-first tests for SetTaskStatus lifecycle mapping to issue state, API failure wrapping, and deterministic SetTaskData comment writes. Implemented SetTaskStatus via authenticated PATCH /issues/{number} with contract status mapping (closed->closed, all others->open), SetTaskData via sorted key=value POST comments, plus shared JSON request helper for authenticated GitHub REST writes. Validation: go test ./internal/github -count=1; go test ./... -count=1.
