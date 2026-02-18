---
id: yr-pgbb
status: open
deps: [yr-fx2p]
links: []
created: 2026-02-10T00:12:26Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T7 Manage AgentSession externalUrls

STRICT TDD: failing tests first. Set/update externalUrls to runner session/log links and preserve uniqueness semantics.

## Acceptance Criteria

Given active session, when execution updates occur, then externalUrls are visible and valid for current run context.


## Notes

**2026-02-18T20:54:33Z**

review_fail_feedback=Update `agentSessionUpdate` to send `id` as a top-level mutation argument (not inside `AgentSessionUpdateInput`), enforce `externalUrls` deduplication by URL (not label+URL), and add tests that assert both the exact GraphQL mutation/variables shape and duplicate-URL-with-different-label handling.

**2026-02-18T20:54:33Z**

review_feedback=Update `agentSessionUpdate` to send `id` as a top-level mutation argument (not inside `AgentSessionUpdateInput`), enforce `externalUrls` deduplication by URL (not label+URL), and add tests that assert both the exact GraphQL mutation/variables shape and duplicate-URL-with-different-label handling.

**2026-02-18T20:54:33Z**

review_retry_count=1

**2026-02-18T20:54:33Z**

review_verdict=fail

**2026-02-18T20:59:37Z**

auto_commit_sha=7a1fa71ee912d04b1c2d091a090e857cc477c47d

**2026-02-18T20:59:37Z**

landing_status=blocked

**2026-02-18T20:59:37Z**

triage_reason=git merge --no-ff task/yr-pgbb failed: Auto-merging cmd/yolo-linear-worker/main_test.go
CONFLICT (content): Merge conflict in cmd/yolo-linear-worker/main_test.go
Auto-merging cmd/yolo-linear-worker/session_processor.go
CONFLICT (content): Merge conflict in cmd/yolo-linear-worker/session_processor.go
Auto-merging cmd/yolo-linear-worker/session_processor_test.go
CONFLICT (content): Merge conflict in cmd/yolo-linear-worker/session_processor_test.go
Automatic merge failed; fix conflicts and then commit the result.: exit status 1

**2026-02-18T20:59:37Z**

triage_status=blocked

**2026-02-18T21:54:58Z**

review_feedback=review verdict returned fail

**2026-02-18T21:54:58Z**

review_retry_count=1

**2026-02-18T21:54:58Z**

review_verdict=fail
