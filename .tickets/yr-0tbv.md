---
id: yr-0tbv
status: open
deps: [yr-fx2p]
links: []
created: 2026-02-10T00:12:27Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T10 Error taxonomy and user-facing remediation for Linear sessions

STRICT TDD: failing tests first. Map webhook/auth/GraphQL/runtime failures to meaningful activity/error messages and stderr logs.

## Acceptance Criteria

Given common failure modes, when errors occur, then both Linear activity and local stderr include specific remediation.


## Notes

**2026-02-18T20:50:36Z**

review_fail_feedback=`cmd/yolo-linear-worker/session_processor.go:171` and `cmd/yolo-linear-worker/session_processor.go:184` only consider `runErr != nil`, so common runtime failures are silently treated as success because backend adapters return failed/blocked `RunnerResult` with `err=nil` (`internal/codex/runner_adapter.go:160`, `internal/opencode/runner_adapter.go:101`, `internal/claude/runner_adapter.go:160`, `internal/kimi/runner_adapter.go:160`); required fix: treat non-`completed` `RunnerResult.Status` as failure (using `result.Reason`), route that through `FormatLinearSessionActionableError` for Linear response activity and stderr-returned errors, and add tests covering failed/blocked `RunnerResult` with nil error.

**2026-02-18T20:50:36Z**

review_feedback=`cmd/yolo-linear-worker/session_processor.go:171` and `cmd/yolo-linear-worker/session_processor.go:184` only consider `runErr != nil`, so common runtime failures are silently treated as success because backend adapters return failed/blocked `RunnerResult` with `err=nil` (`internal/codex/runner_adapter.go:160`, `internal/opencode/runner_adapter.go:101`, `internal/claude/runner_adapter.go:160`, `internal/kimi/runner_adapter.go:160`); required fix: treat non-`completed` `RunnerResult.Status` as failure (using `result.Reason`), route that through `FormatLinearSessionActionableError` for Linear response activity and stderr-returned errors, and add tests covering failed/blocked `RunnerResult` with nil error.

**2026-02-18T20:50:36Z**

review_retry_count=1

**2026-02-18T20:50:36Z**

review_verdict=fail

**2026-02-18T20:58:34Z**

review_fail_feedback=`cmd/yolo-linear-worker/error_taxonomy.go:15` matches `queued job` as `webhook` before other classes, and `internal/linear/webhook/worker.go:148` wraps all processor errors as `process queued job ...`, so stderr currently misclassifies wrapped runtime/auth/graphql failures as `webhook` with incorrect remediation (repro: wrapped `run linear session job: opencode stall...` and wrapped `agent activity mutation http 401...` both produce `Category: webhook`); required fixes: narrow/remove the broad `queued job` webhook matcher or reorder taxonomy precedence so auth/graphql/runtime can win for wrapped processor errors, and add regression tests for wrapped runtime/auth/graphql stderr classification.

**2026-02-18T20:58:34Z**

review_feedback=`cmd/yolo-linear-worker/error_taxonomy.go:15` matches `queued job` as `webhook` before other classes, and `internal/linear/webhook/worker.go:148` wraps all processor errors as `process queued job ...`, so stderr currently misclassifies wrapped runtime/auth/graphql failures as `webhook` with incorrect remediation (repro: wrapped `run linear session job: opencode stall...` and wrapped `agent activity mutation http 401...` both produce `Category: webhook`); required fixes: narrow/remove the broad `queued job` webhook matcher or reorder taxonomy precedence so auth/graphql/runtime can win for wrapped processor errors, and add regression tests for wrapped runtime/auth/graphql stderr classification.

**2026-02-18T20:58:34Z**

review_retry_count=2

**2026-02-18T20:58:34Z**

review_verdict=fail

**2026-02-18T21:02:44Z**

auto_commit_sha=63a7697fb7e720679ba58c8af80bbd34fe9923c9

**2026-02-18T21:02:44Z**

landing_status=blocked

**2026-02-18T21:02:44Z**

triage_reason=git merge --no-ff task/yr-0tbv failed: Auto-merging cmd/yolo-linear-worker/main_test.go
Auto-merging cmd/yolo-linear-worker/session_processor.go
CONFLICT (content): Merge conflict in cmd/yolo-linear-worker/session_processor.go
Auto-merging cmd/yolo-linear-worker/session_processor_test.go
CONFLICT (content): Merge conflict in cmd/yolo-linear-worker/session_processor_test.go
Automatic merge failed; fix conflicts and then commit the result.: exit status 1

**2026-02-18T21:02:44Z**

triage_status=blocked
