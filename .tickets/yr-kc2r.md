---
id: yr-kc2r
status: closed
deps: [yr-fcft, yr-r3hr]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T7 Document validate/init workflow and troubleshooting

Document command usage, precedence, common failures, and remediation in README/runbook.

## Acceptance Criteria

Docs include validate/init examples and troubleshooting; docs/tests are updated through strict TDD-style doc-test cycle with proof commands in notes.

## Notes

**2026-02-16T08:41:57Z**

tdd_red_command=`go test ./internal/docs -run TestConfigWorkflowDocsCoverValidateInitAndTroubleshooting -count=1`
tdd_red_exit=1
tdd_red_result=failed as expected before docs update (`README missing validate/init workflow guidance`).

**2026-02-16T08:41:57Z**

tdd_green_command=`go test ./internal/docs -run TestConfigWorkflowDocsCoverValidateInitAndTroubleshooting -count=1`
tdd_green_exit=0
tdd_green_result=pass after documenting `yolo-agent config init/validate` workflow in `README.md` and adding `docs/config-workflow.md` troubleshooting runbook.

**2026-02-16T08:41:57Z**

tdd_broader_command=`go test ./internal/docs -count=1`
tdd_broader_exit=0
tdd_refactor_confirmation=no additional refactor required; docs changes are minimal and scoped to workflow/troubleshooting coverage.

**2026-02-16T08:44:37Z**

review_fail_feedback=Correct the runbook precedence section to match actual `config validate` behavior (or implement backend-precedence validation in the command), and add/adjust doc tests so they verify this behavior-level accuracy rather than only string presence.

**2026-02-16T08:44:37Z**

review_feedback=Correct the runbook precedence section to match actual `config validate` behavior (or implement backend-precedence validation in the command), and add/adjust doc tests so they verify this behavior-level accuracy rather than only string presence.

**2026-02-16T08:44:37Z**

review_retry_count=1

**2026-02-16T08:44:37Z**

review_verdict=fail

**2026-02-16T08:49:53Z**

review_fail_feedback=Correct the runbook precedence/troubleshooting text to reflect actual behavior (`YOLO_AGENT_BACKEND` is ignored by `config validate`, but `--agent-backend`/`--backend` are unsupported and fail flag parsing), then update `internal/docs/workflow_test.go` to assert the corrected wording and add/adjust coverage for unknown backend flag handling so docs and tests match real CLI behavior.

**2026-02-16T08:49:53Z**

review_verdict=fail

**2026-02-16T08:49:53Z**

triage_reason=review rejected: Correct the runbook precedence/troubleshooting text to reflect actual behavior (`YOLO_AGENT_BACKEND` is ignored by `config validate`, but `--agent-backend`/`--backend` are unsupported and fail flag parsing), then update `internal/docs/workflow_test.go` to assert the corrected wording and add/adjust coverage for unknown backend flag handling so docs and tests match real CLI behavior.

**2026-02-16T08:49:53Z**

triage_status=failed

**2026-02-18T08:23:02Z**

review_fail_feedback=`docs/config-workflow.md:36` (enforced by `internal/docs/workflow_test.go:191`) says `--agent-backend`/`--backend` are ignored by `yolo-agent config validate`, but runtime behavior is a hard parse error (`flag provided but not defined: -backend`); align docs/tests with actual behavior (flags unsupported) or change the command to accept and ignore those flags so documentation matches implementation.

**2026-02-18T08:23:02Z**

review_feedback=`docs/config-workflow.md:36` (enforced by `internal/docs/workflow_test.go:191`) says `--agent-backend`/`--backend` are ignored by `yolo-agent config validate`, but runtime behavior is a hard parse error (`flag provided but not defined: -backend`); align docs/tests with actual behavior (flags unsupported) or change the command to accept and ignore those flags so documentation matches implementation.

**2026-02-18T08:23:02Z**

review_retry_count=1

**2026-02-18T08:23:02Z**

review_verdict=fail
