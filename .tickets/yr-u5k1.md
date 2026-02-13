---
id: yr-u5k1
status: closed
deps: []
links: []
created: 2026-02-10T00:12:27Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T9 Single-workspace auth/profile and config

STRICT TDD: failing tests first. Add single-workspace Linear app credentials/profile validation and startup diagnostics.

## Acceptance Criteria

Given single-workspace config, when service starts, then auth/config are validated with actionable errors.


## Notes

**2026-02-13T22:33:32Z**

Implemented strict TDD updates for single-workspace Linear startup validation in yolo-agent: actionable config-path guidance for missing workspace/token env, export hint for missing token value, and rejection of multi-workspace values in linear.scope.workspace. Added startup-level RunMain test asserting auth_profile_config actionable output. Validation: go test ./cmd/yolo-agent; go test ./... -timeout 180s. Commit: 5ccfdca.
