---
id: yr-73gx
status: closed
deps: [yr-7s41]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Watchdog for no-output stalls (even with timeout=0)

Ensure yolo-agent cannot hang indefinitely on true stalls.

Implementation sketch:
- Integrate internal/opencode Watchdog into the runner path (RunWithACP or CLIRunnerAdapter) so that:
  - when the runner log (ACP updates) has no new output for > threshold,
  - the subprocess is killed,
  - and the error is classified as StallError with category permission/question/no_output.
- Make the threshold configurable (via request metadata or a default).
- Add unit tests with a mock Process + fake log activity to prove kill+classification.

Rationale: yolo-agent defaults runner_timeout=0 today; watchdog must still prevent infinite hangs.

## Acceptance Criteria

Given runner_timeout is 0,
when the agent produces no ACP updates for longer than the watchdog threshold,
then the subprocess is terminated and the ticket is marked blocked with a reason containing opencode stall category=... .

Given a permission or question prompt is the cause,
when watchdog classifies the stall,
then the category is permission or question (not no_output).


## Notes

**2026-02-11T13:39:18Z**

Completed: integrated watchdog into RunWithACP path; watchdog timeout/interval now configurable via RunnerRequest metadata and enforced even when runner-timeout=0.
