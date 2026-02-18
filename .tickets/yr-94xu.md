---
id: yr-94xu
status: closed
deps: [yr-10ub]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T3 Extract shared config load and semantic validation service

Centralize .yolo-runner/config.yaml load, parse, and semantic validation for reuse by runtime and config validate command.

## Acceptance Criteria

Missing file, invalid YAML, unknown fields, bad durations/numbers, unsupported backend, and profile/auth validation are covered via strict TDD and passing tests are recorded in notes.


## Notes

**2026-02-15T20:19:30Z**

auto_commit_sha=ee045275ca03f43120382f46bc5899e6b0e8bf37

**2026-02-15T20:19:30Z**

landing_status=blocked

**2026-02-15T20:19:30Z**

triage_reason=git checkout main failed: error: you need to resolve your current index first
.tickets/yr-94xu.md: needs merge: exit status 1

**2026-02-15T20:19:30Z**

triage_status=blocked

**2026-02-16T07:12:10Z**

TDD RED: go test ./cmd/yolo-agent -count=1 (failed as expected before implementation: undefined newTrackerConfigService in config_service_test.go).

**2026-02-16T07:12:13Z**

TDD GREEN: go test ./cmd/yolo-agent -count=1 (pass) and go test ./... -count=1 (pass) after adding shared trackerConfigService and refactoring callers.
