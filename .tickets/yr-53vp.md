---
id: yr-53vp
status: closed
deps: []
links: []
created: 2026-02-11T13:09:53Z
type: epic
priority: 0
assignee: Gennady Evstratov
parent: yr-s0go
---
# E2: Eliminate yolo-agent hangs

yolo-agent frequently hangs during task execution (especially for E2 tickets): the run stalls with runner_started but never reaches runner_finished.

Primary hypotheses:
- OpenCode/ACP session reaches idle (session.prompt exiting loop / session.idle) but the ACP transport stays open (stdout remains open due to long-lived process / file watchers), so the client waits forever.
- The agent blocks on interactive input (permission prompts, question prompts, provider selection, auth).
- A true no-output stall occurs and no timeout/watchdog terminates the subprocess.

This epic defines a debugging + hardening plan to ensure every runner invocation terminates deterministically (completed/blocked/failed) and emits enough telemetry to triage quickly.

Out of scope: completing backend adapter feature work (handled by E2-T1..T7).

## Acceptance Criteria

Given yolo-agent runs against a root epic,
when a runner invocation completes a prompt turn but the opencode subprocess keeps stdout open,
then the runner returns a RunnerResult without hanging.

Given the runner produces no ACP updates for longer than a configured threshold,
when the threshold is exceeded,
then the subprocess is terminated and the task is marked blocked with a classified reason.

Given permission/question prompts occur,
when running in CI/non-interactive mode,
then the prompts are handled deterministically (no user input required) and the runner does not hang.
