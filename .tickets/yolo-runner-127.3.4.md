---
id: yolo-runner-127.3.4
status: closed
deps: []
links: []
created: 2026-01-30T09:19:13.84906426Z
type: task
priority: 1
parent: yolo-runner-127.3
---
# v1.2: Detect OpenCode race conditions and add timeout handling

OpenCode got stuck indefinitely when Serena language server failed to initialize. The runner process had to be manually killed after 58k seconds.

**Problem:**
- Serena language server failed to initialize (error: 'language server manager is not initialized')
- OpenCode was told to 'wait for instructions' but never detected the failure
- Process ran for 58k+ seconds before manual termination
- No automatic timeout or retry mechanism

**Symptoms:**
- Session stuck at step 39 without progress
- Process consuming CPU/memory but no completion
- Error visible in opencode stderr logs but not propagated to exit code

**Root Cause:**
OpenCode's Serena integration has a race condition where if language server fails during project activation, it waits indefinitely instead of propagating the error and terminating the session.

**Acceptance Criteria:**
- Add timeout mechanism to detect stuck OpenCode processes
- Propagate Serena initialization failures to exit code immediately
- Log clear error messages when language server fails to start
- Add health check for OpenCode session progress
- Kill stuck subprocesses after timeout threshold
- Tests verify timeout behavior works correctly

## Acceptance Criteria

- Add timeout mechanism to detect stuck OpenCode processes
- Propagate Serena initialization failures to exit code immediately
- Log clear error messages when language server fails to start
- Add health check for OpenCode session progress
- Kill stuck subprocesses after timeout threshold
- Tests verify timeout behavior works correctly


