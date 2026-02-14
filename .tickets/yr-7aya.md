---
id: yr-7aya
status: closed
deps: [yr-iedw]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 3
assignee: Gennady Evstratov
parent: yr-cy9b
---
# E9-T1 Record Yandex defer rationale and contract extension points

Document explicit defer decision and required adapter extension points for future implementation.

## Acceptance Criteria

Given roadmap docs, when reviewed, then Yandex is explicitly deferred with clear future entry points.


## Notes

**2026-02-13T23:58:50Z**

Implemented E9 Yandex defer documentation and roadmap contract checks. Updated V2_IMPROVEMENTS.md with explicit defer rationale (no runtime integration this wave) and concrete future extension points: tracker profile model/switch wiring, contracts.TaskManager implementation path, and task-manager conformance suite coverage. Added internal/docs/yandex_defer_test.go to enforce these roadmap requirements. Validation: go test ./internal/docs -count=1; go test ./... -count=1.
