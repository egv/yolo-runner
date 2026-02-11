---
id: yr-3izt
status: closed
deps: []
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Regression test for idle + stdout-open

Add a deterministic test that reproduces the suspected hang where the ACP prompt completes but the underlying opencode process keeps stdout open.

Implementation sketch:
- In internal/opencode tests, simulate an agent that:
  - responds to initialize/session/new/session/prompt,
  - emits DONE for verification,
  - but intentionally never closes stdout.
- Assert RunACPClient returns within a bounded time window (sub-second).

This test must fail on the buggy implementation and pass after the fix.

## Acceptance Criteria

Given the simulated agent keeps stdout open,
when RunACPClient finishes the verification step,
then RunACPClient returns without waiting for the read loop to exit.

Given the test suite,
when running go test ./...,
then the regression test passes and completes quickly.


## Notes

**2026-02-11T13:39:18Z**

Completed: added stdout-open regression test in internal/opencode/acp_client_test.go to ensure RunACPClient returns when ACP prompt ends.
