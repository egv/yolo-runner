---
id: yr-cjx6
status: closed
deps: [yr-7s41]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Non-interactive permission/question hardening

Eliminate hangs caused by interactive prompts.

Scope:
- Verify OPENCODE_PERMISSION and other CI flags prevent permission prompts from blocking.
- Ensure ACP RequestPermission handling is deterministic:
  - auto-allow safe operations,
  - for question-like prompts, cancel the tool call and inject a deterministic response.
- Ensure the failure mode is observable (events + blocked reason) when an auth/provider prompt occurs.

Add targeted tests that simulate permission/question request flows.

## Acceptance Criteria

Given an ACP permission request,
when running in CI mode,
then the handler returns a deterministic decision without waiting for user input.

Given a question-like prompt,
when the agent requests permission,
then the runner does not hang and the session progresses (or fails) deterministically.


## Notes

**2026-02-11T13:39:18Z**

Completed: hardened non-interactive behavior with deterministic OPENCODE_PERMISSION policy tests and question/provider stall classification coverage.
