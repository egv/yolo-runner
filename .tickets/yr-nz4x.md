---
id: yr-nz4x
status: closed
deps: [yr-73gx]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Runner-timeout defaults + documentation

Decide and document sane timeout defaults so hangs are bounded in real runs.

Options:
- Keep --runner-timeout default 0 but rely on watchdog.
- Or set a non-zero default (e.g. 30m/45m) and still keep watchdog as a safety net.

Deliverables:
- Update docs (README or docs/hang-triage.md) with recommended profiles:
  - local dev run
  - CI run
  - long tasks
- Add a small CLI test to assert the chosen default is wired correctly.

## Acceptance Criteria

Given yolo-agent is run without explicitly setting --runner-timeout,
when the runner stalls,
then the run terminates in a bounded amount of time (timeout and/or watchdog) and does not hang indefinitely.

Given docs,
when scanning for runner-timeout guidance,
then the recommended profiles are present and specific.


## Notes

**2026-02-11T13:39:18Z**

Completed: documented timeout profiles including default watchdog behavior and added CLI tests for runner/watchdog defaults and flags.
