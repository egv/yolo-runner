# New features for yolo runner

Here are features for new version

## support for different issue trackers
- linear
- github
- startrek (yandex tracker) - deferred in E9 (no runtime integration in this wave)

### Yandex defer rationale (E9)
- E9 explicitly keeps Yandex/Startrek out of runtime scope so the current wave can stabilize and release the existing tracker set (`tk`, `linear`, `github`) with conformance and self-host acceptance coverage.
- This avoids widening auth/scope/API surface area during the same release gate used for multi-backend + multi-tracker hardening.
- Decision: keep Yandex documented as planned, but do not add runtime profile parsing, API calls, or task mutation paths in this wave.

### Yandex contract extension points (future implementation)
- `cmd/yolo-agent/tracker_profile.go`: add `trackerTypeStartrek`, add a typed `startrek` profile model, and validate required scope/auth fields under `tracker.startrek`.
- `cmd/yolo-agent/tracker_profile.go` (`buildTaskManagerForTracker`): wire a `startrek` branch that constructs the new adapter.
- `internal/contracts/contracts.go`: implement the existing `contracts.TaskManager` interface for Startrek; no task-loop contract changes are required for MVP integration.
- `internal/contracts/conformance/task_manager_suite.go`: run the shared tracker conformance suite for the new Startrek task manager before enabling it in release profiles.
- `cmd/yolo-agent/tracker_profile_test.go` and tracker profile/e2e tests: add failing-first coverage for `tracker.type: startrek`, auth errors, scope validation, and happy-path wiring.

## support for different agents
- codex
- claude
- kimi

## support for new vcs
- arc vcs

## planning enhancements
Right now we run ohnly one runner and implement tasks in order. We should learn how to figure out series of tasks that can be executed in parallel and do so with a given level of concurrency in separate working copies of a repository (this should work only for git now). Probably we should add some locking mechanisms, so we can gateway tasks implementation (i.e. task 3 depends on both task 1 and task 2, so we should execute 1 and 2 in paralles, wait while both of them are completed and then continue to task 3)

## linear agent
Implement linear agent so runner can be used to delegate linear tasks (including epics). More info here: https://linear.app/developers/aig
