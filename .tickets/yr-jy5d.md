---
id: yr-jy5d
status: open
deps: [yr-o4sq, yr-09pb, yr-x1rh, yr-iija, yr-qmru, yr-uby7]
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T22 End-to-end stdin GUI contract tests

STRICT TDD: add e2e and integration tests that feed NDJSON via stdin and verify state transitions, metrics, interaction behavior, and deterministic rendering semantics.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. E2E stdin contract tests must be written first and must validate deterministic rendering/state transitions with Bubble Tea model.

**2026-02-10T08:25:05Z**

Error-state design mandatory: E2E tests must include malformed events, decode failures, transition conflicts, and renderer-safe fallback behavior.
