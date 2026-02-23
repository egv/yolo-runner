# Runner Build Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Restore a clean build by fixing runner implementation gaps and aligning imports so `go test ./...` passes.

**Architecture:** The runner should own progress heartbeat, stop handling, and git cleanup by wiring existing helpers in `internal/runner` and UI in `internal/ui`. We will add missing option fields, use existing stop/cleanup helpers, and align imports with the module path.

**Tech Stack:** Go, standard library `context`, existing `internal/runner`, `internal/ui`.

### Task 1: Capture current runner state and failing tests

**Files:**
- Read: `internal/runner/runner.go`
- Read: `internal/runner/heartbeat_test.go`
- Read: `internal/runner/stop_test.go`

**Step 1: Read runner implementation**

Use Read tool on `internal/runner/runner.go` to understand current fields/functions.

**Step 2: Read heartbeat tests**

Use Read tool on `internal/runner/heartbeat_test.go` to list required `RunOnceOptions` fields and expected progress behavior.

**Step 3: Read stop tests**

Use Read tool on `internal/runner/stop_test.go` to list required stop/cleanup behavior and helper usage.

### Task 2: Add missing options, imports, and update call sites

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/heartbeat_test.go`
- Modify: `internal/runner/stop_test.go`

**Step 1: Write failing compile expectations**

Run: `go test ./internal/runner -run TestRunOncePrintsOpenCodeHeartbeat -v`
Expected: FAIL with missing fields/imports (record exact errors).

**Step 2: Add missing fields to RunOnceOptions**

Update `RunOnceOptions` struct to include:
- `ProgressNow func() time.Time`
- `ProgressTicker func() (<-chan time.Time, func())`
- `Stop <-chan struct{}`
- `CleanupConfirm func(context.Context) error`
- `StatusPorcelain func(context.Context) (string, error)`
- `GitRestoreAll func(context.Context) error`
- `GitCleanAll func(context.Context) error`

**Step 3: Add imports**

Ensure `internal/runner/runner.go` imports:
- `context`
- `github.com/egv/yolo-runner/internal/ui`

**Step 4: Update call sites for new option types**

Adjust tests to use the new option shapes:
- `internal/runner/heartbeat_test.go`: pass `ProgressTicker` as `func() (<-chan time.Time, func())` (wrap `fakeProgressTicker` into the factory) in `RunOnceOptions`.
- `internal/runner/stop_test.go`: pass `Stop` as `<-chan struct{}` in `RunOnceOptions` and use a channel to trigger stop.

**Step 5: Run compile test**

Run: `go test ./internal/runner -run TestRunOncePrintsOpenCodeHeartbeat -v`
Expected: FAIL with missing behavior (not compile errors).

### Task 3: Wire progress heartbeat lifecycle

**Files:**
- Modify: `internal/runner/runner.go`

**Step 1: Write failing test**

Run: `go test ./internal/runner -run TestRunOncePrintsOpenCodeHeartbeat -v`
Expected: FAIL with heartbeat/progress assertions.

**Step 2: Implement heartbeat start/stop**

In `RunOnce`, before invoking OpenCode:
- Print the state line: `State: opencode running` with a newline.
- Create progress with `ui.NewProgress(ui.ProgressConfig{Writer: out, State: "opencode running", LogPath: opts.LogPath, Ticker: progressTickerFrom(opts.ProgressTicker), Now: opts.ProgressNow})`.
- Start the heartbeat with `go progress.Run(progressCtx)` using a cancelable context tied to the stop context.
- Call `progress.Finish()` after OpenCode completes or errors.

Ensure defaults are safe when `ProgressNow`/`ProgressTicker` are nil (delegate to `ui.NewProgress`).

**Step 3: Run test**

Run: `go test ./internal/runner -run TestRunOncePrintsOpenCodeHeartbeat -v`
Expected: PASS

### Task 4: Wire stop/cleanup flow using existing helpers

**Files:**
- Read: `internal/runner/stop.go`
- Modify: `internal/runner/runner.go`

**Step 1: Read stop helpers**

Use Read tool on `internal/runner/stop.go` to confirm `StopState` and `CleanupAfterStop` usage patterns.

**Step 2: Implement stop monitoring**

In `RunOnce`, create `StopState` using `StopStateOptions`:
- `Stop: opts.Stop`
- `CleanupConfirm: opts.CleanupConfirm`
- `StatusPorcelain: opts.StatusPorcelain`
- `GitRestoreAll: opts.GitRestoreAll`
- `GitCleanAll: opts.GitCleanAll`

Ensure it uses defaults when opts fields are nil (follow existing defaults in `stop.go`).

**Step 3: Cleanup on stop**

When stop state is triggered (per helpers), call `CleanupAfterStop` and return the stop error.

**Step 4: Run stop tests**

Run: `go test ./internal/runner -run TestStop -v`
Expected: PASS

### Task 5: Confirm imports and full test suite

**Files:**
- Read: `go.mod`
- Read: `cmd/yolo-runner/main.go`
- Read: `internal/runner/runner.go`

**Step 1: Verify module path**

Use Read tool on `go.mod` to confirm module path is `github.com/egv/yolo-runner`.

**Step 2: Search for incorrect imports**

Run: `rg "yolo-runner/" -n` to ensure no `yolo-runner/...` imports remain.

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS

### Task 6: Commit and push (post-tests)

**Files:**
- Add: `internal/runner/runner.go`
- Add: `internal/prompt/prompt.go`
- Add: `internal/prompt/prompt_test.go`
- Add: `AGENTS.md`
- Add: deletions of root-level v1 Go files
- Add: `go.mod`, `go.sum`

**Step 1: Git status and add**

Run: `git status` and `git add` for modified files.

**Step 2: Commit**

Run: `git commit -m "fix: restore runner build and tests"`

**Step 3: Push**

Run: `git pull --rebase`, `bd sync`, `git push`, `git status` (must be up to date).
