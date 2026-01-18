# Go Standalone Runner Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace `beads_yolo_runner.py` with a Go program that builds to a single standalone binary and runs one bead task at a time by shelling out to `bd` and `opencode`.

**Architecture:** Keep integrations thin and explicit: a Go "runner" orchestrates `bd` and `opencode` via `os/exec`, parses JSON responses, writes logs, and commits/updates task state. Structure the code so later versions can swap integrations (task tracker, coding agent, and eventually VCS) behind interfaces without rewriting the core loop.

**Tech Stack:** Go 1.25+, `os/exec`, `encoding/json`, `net/http` (reserved for v2), `testing` (Go stdlib), Git CLI.

## Scope

### v1 (this plan)

- Go CLI that replicates current Python behavior:
  - Pick next open leaf task under a root bead
  - Mark it `in_progress`
  - Build prompt from bead title/description/acceptance
  - Run `opencode run` against repo root using YOLO agent
  - Capture OpenCode output to per-task JSONL log
  - If no git changes: mark task `blocked` and exit
  - Else: commit, close bead, verify closed, `bd sync`
- New runner flag: `--model <id>` forwarded to OpenCode.
- Produce a single binary via `go build`.

### v2 (note these as beads tasks; do not implement yet)

- Abstract task tracker and coding agent so runner can work with alternatives (Beads/OpenCode become plugins/adapters).
- Add a small webserver that serves progress status and recent logs.

### v3 (note these as beads tasks; do not implement yet)

- Abstract VCS operations (git becomes a pluggable adapter).

## Design Notes

### Why agent is fixed in v1

The `yolo` agent definition (from `yolo.md` mirrored into `.opencode/agent/yolo.md`) is part of the product contract: it enforces non-interactive behavior and strict scope rules (no `bd` calls, no clarification questions). Because changing the agent can silently break automation guarantees, v1 does not expose an `--agent` flag.

### CLI contract (v1)

Command name proposal: `yolo-runner`

Flags:
- `--repo` (default `.`): path to repo root
- `--root` (default `algi-8bt`): root bead/epic id
- `--max` (optional): max tasks to process (0/omitted means loop until no tasks)
- `--dry-run`: print selected task + prompt + computed commands, do not execute
- `--model` (optional): OpenCode model id to pass through

Note: v1 hardcodes the OpenCode agent name to `yolo` (see OpenCode invocation section).

### OpenCode invocation (v1)

Equivalent to current Python, plus optional model:

- Base: `opencode run <prompt> --agent <agent> --format json <repoRoot>`
- With model: `opencode run <prompt> --agent <agent> --format json --model <model> <repoRoot>`

In v1 we hardcode `<agent>` to `yolo`.
Also ensure the agent file exists at `.opencode/agent/yolo.md` (see file layout section).

If OpenCode’s model flag differs, treat it as a bug to resolve during implementation by updating the adapter and tests.

### Files and layout (v1)

- Keep `beads_yolo_runner.py` initially; add Go implementation alongside.
- Ensure the OpenCode agent definition is available to `opencode run --agent yolo`:
  - Current agent file is `yolo.md` (repo root).
  - v1 should add a tracked copy at `.opencode/agent/yolo.md` so OpenCode can reliably discover it in-repo.
  - Prefer keeping `yolo.md` as the source of truth and copying/syncing it into `.opencode/agent/yolo.md` (or replace `yolo.md` with a short stub that points people to `.opencode/agent/yolo.md`).
- New paths:
  - `cmd/yolo-runner/main.go`
  - `internal/runner/` core loop + orchestration
  - `internal/beads/` beads adapter (shells out to `bd`)
  - `internal/opencode/` opencode adapter (shells out to `opencode`)
  - `internal/vcs/git/` git adapter (shells out to `git`) — this is still git-specific in v1
  - `internal/logging/` JSONL logging helpers
  - `internal/prompt/` prompt builder
  - `.opencode/agent/yolo.md`

### Behavior parity checklist

Match current `beads_yolo_runner.py` behaviors:
- Leaves selection uses: `bd ready --parent <root> --json` recursively until first open leaf task.
- `bd show <id> --json` returns array; runner uses index 0.
- Writes per-task OpenCode output to: `runner-logs/opencode/<issue-id>.jsonl` (overwrite each run).
- Appends runner summary log: `runner-logs/beads_yolo_runner.jsonl`.
- Uses `git status --porcelain` to detect whether OpenCode produced changes.

## Test Strategy (v1)

Use stdlib `testing` with fake command runner (no real subprocess calls in unit tests).

Core pattern:
- Introduce a small interface:

```go
type CommandRunner interface {
    Output(name string, args ...string) ([]byte, error)
    Run(name string, args ...string) error
}
```

- Unit tests inject a fake runner that:
  - Records commands
  - Returns canned JSON outputs for `bd ready` / `bd show`
  - Simulates git dirty/clean states

Integration smoke tests (optional but recommended once basic unit tests pass):
- Build binary
- Run `--dry-run` in repo

## Implementation Tasks

### Task 1: Create Go module and skeleton CLI

**Files:**
- Create: `go.mod`
- Create: `cmd/yolo-runner/main.go`

**Step 0: Ensure YOLO agent file is in the expected location**

Create: `.opencode/agent/yolo.md` (copy content from `yolo.md`)

Run: `mkdir -p .opencode/agent && cp yolo.md .opencode/agent/yolo.md`
Expected: file exists and contains the same frontmatter + instructions as `yolo.md`

**Step 1: Write the failing test**

Create: `cmd/yolo-runner/main_test.go`

```go
package main

import "testing"

func TestParseFlags_Defaults(t *testing.T) {
    cfg, err := parseArgs([]string{})
    if err != nil {
        t.Fatalf("expected nil err, got %v", err)
    }
    if cfg.Repo != "." {
        t.Fatalf("expected repo '.', got %q", cfg.Repo)
    }
    if cfg.Root != "algi-8bt" {
        t.Fatalf("expected root 'algi-8bt', got %q", cfg.Root)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL with `undefined: parseArgs`

**Step 3: Write minimal implementation**

In `cmd/yolo-runner/main.go` implement:
- `type Config struct { Repo, Root, Model string; Max int; DryRun bool }`
- `func parseArgs(args []string) (Config, error)` using `flag.FlagSet`
- `main()` calls runner package later

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add go.mod cmd/yolo-runner/main.go cmd/yolo-runner/main_test.go
git commit -m "feat: add Go CLI skeleton"
```

### Task 2: Add command runner abstraction

**Files:**
- Create: `internal/execx/execx.go`
- Create: `internal/execx/execx_test.go`

**Step 1: Write the failing test**

```go
package execx

import "testing"

func TestFakeRunner_RecordsCommands(t *testing.T) {
    r := NewFakeRunner()
    _ = r.Run("git", "status")

    got := r.Commands()
    if len(got) != 1 {
        t.Fatalf("expected 1 command, got %d", len(got))
    }
    if got[0].Name != "git" {
        t.Fatalf("expected git, got %q", got[0].Name)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL with `undefined: NewFakeRunner`

**Step 3: Write minimal implementation**

Implement in `internal/execx/execx.go`:
- `type Cmd struct{ Name string; Args []string }`
- `type Runner interface { Output(name string, args ...string) ([]byte, error); Run(name string, args ...string) error }`
- `type FakeRunner` with:
  - `Run(...)` appends cmd, returns configured error
  - `Output(...)` appends cmd, returns configured bytes
  - Methods to set per-command outputs (`WhenOutput(name,args...).Return(bytes)` style is fine but keep simple)

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/execx/execx.go internal/execx/execx_test.go
git commit -m "test: add fake command runner"
```

### Task 3: Implement beads adapter (shell out to bd)

**Files:**
- Create: `internal/beads/client.go`
- Create: `internal/beads/client_test.go`

**Step 1: Write the failing test**

```go
package beads

import (
    "context"
    "testing"

    "yolo-runner/internal/execx"
)

func TestClient_ShowIssue_ParsesFields(t *testing.T) {
    r := execx.NewFakeRunner()
    r.SetOutput("bd", []string{"show", "yolo-runner-abc", "--json"}, []byte(`[
      {"id":"yolo-runner-abc","title":"T","description":"D","acceptance_criteria":"A"}
    ]`))

    c := NewClient(r)
    issue, err := c.Show(context.Background(), "yolo-runner-abc")
    if err != nil {
        t.Fatalf("err: %v", err)
    }
    if issue.ID != "yolo-runner-abc" || issue.Title != "T" || issue.Description != "D" || issue.Acceptance != "A" {
        t.Fatalf("unexpected issue: %#v", issue)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL undefined `NewClient`

**Step 3: Write minimal implementation**

Implement:
- `type Issue struct { ID, Title, Description, Acceptance string }`
- `type Client struct { r execx.Runner }`
- `Show(ctx,id)` executes: `bd show <id> --json`, parses array, returns first.
- `ReadyChildren(ctx,parentID)` executes: `bd ready --parent <parent> --json`, returns parsed list.
- `UpdateStatus(ctx,id,status)` executes: `bd update <id> --status <status>`
- `Close(ctx,id)` executes: `bd close <id>`
- `Sync(ctx)` executes: `bd sync`

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/beads/client.go internal/beads/client_test.go
git commit -m "feat: add beads CLI adapter"
```

### Task 4: Implement leaf task selection logic

**Files:**
- Create: `internal/runner/select.go`
- Create: `internal/runner/select_test.go`

**Step 1: Write the failing test**

```go
package runner

import (
    "context"
    "testing"

    "yolo-runner/internal/beads"
)

type fakeBeads struct {
    ready map[string][]beads.ReadyItem
}

func (f *fakeBeads) ReadyChildren(ctx context.Context, parent string) ([]beads.ReadyItem, error) {
    return f.ready[parent], nil
}

func TestSelectFirstOpenLeafTaskID(t *testing.T) {
    b := &fakeBeads{ready: map[string][]beads.ReadyItem{
        "root": {
            {ID: "epic1", IssueType: "epic", Status: "open"},
        },
        "epic1": {
            {ID: "task1", IssueType: "task", Status: "open"},
        },
    }}

    got, err := SelectFirstOpenLeafTaskID(context.Background(), b, "root")
    if err != nil {
        t.Fatalf("err: %v", err)
    }
    if got != "task1" {
        t.Fatalf("expected task1, got %q", got)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL with missing types

**Step 3: Write minimal implementation**

- Define `beads.ReadyItem` struct matching `bd ready --json` fields used:
  - `ID`, `IssueType`, `Status`, `Priority`
- Implement `SelectFirstOpenLeafTaskID(ctx, beadsClient, rootID)`:
  - Load children with `ReadyChildren`
  - Sort by `Priority` ascending (missing priority -> large)
  - Skip non-open
  - If `task` return id
  - If `epic` recurse

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/runner/select.go internal/runner/select_test.go internal/beads/client.go
git commit -m "feat: select first open leaf bead task"
```

### Task 5: Implement prompt builder

**Files:**
- Create: `internal/prompt/prompt.go`
- Create: `internal/prompt/prompt_test.go`

**Step 1: Write the failing test**

```go
package prompt

import "testing"

func TestBuild_IncludesTitleAndAcceptance(t *testing.T) {
    got := Build("id1", "My Title", "Desc", "Accept")
    if !contains(got, "Your task is: id1 - My Title") {
        t.Fatalf("missing header")
    }
    if !contains(got, "**Acceptance Criteria:**") || !contains(got, "Accept") {
        t.Fatalf("missing acceptance")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL undefined `Build`

**Step 3: Write minimal implementation**

- `Build(issueID,title,description,acceptance string) string` that matches the Python string exactly (including the strict TDD block).
- In tests, avoid brittle full-string equality; check key substrings.

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go
git commit -m "feat: add prompt builder"
```

### Task 6: Implement opencode adapter (shell out)

**Files:**
- Create: `internal/opencode/client.go`
- Create: `internal/opencode/client_test.go`

**Step 1: Write the failing test**

```go
package opencode

import (
    "context"
    "testing"

    "yolo-runner/internal/execx"
)

func TestBuildCommand_IncludesModelWhenProvided(t *testing.T) {
    r := execx.NewFakeRunner()
    c := NewClient(r)

    cmd := c.BuildRunCommand("/repo", "PROMPT", "yolo", "json", "gpt-4.1") // agent is hardcoded to yolo in v1

    if !cmd.HasArg("--model") || !cmd.HasArg("gpt-4.1") {
        t.Fatalf("expected model args, got %#v", cmd.Args)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL undefined

**Step 3: Write minimal implementation**

- `type Client struct { r execx.Runner }`
- Build args:
  - `run <prompt> --agent <agent> --format <format> [--model <model>] <repoRoot>`
- Implement `Run(ctx, repoRoot, prompt, model string, stdoutPath string) error`:
  - Create parent directories
  - Execute opencode while redirecting stdout to `stdoutPath`
  - Provide env vars equivalent to Python’s `build_opencode_env`:
    - `OPENCODE_DISABLE_CLAUDE_CODE=...` etc.
    - `CI=true`
  - Create per-run config dir (`~/.config/opencode-runner/opencode`) and ensure `opencode.json` exists

Note: This is the one area where an interface for env/config handling will help v2.

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/opencode/client.go internal/opencode/client_test.go
git commit -m "feat: add opencode CLI adapter"
```

### Task 7: Implement git adapter (v1)

**Files:**
- Create: `internal/vcs/git/git.go`
- Create: `internal/vcs/git/git_test.go`

**Step 1: Write the failing test**

```go
package git

import (
    "context"
    "testing"

    "yolo-runner/internal/execx"
)

func TestIsDirty_UsesPorcelain(t *testing.T) {
    r := execx.NewFakeRunner()
    r.SetOutput("git", []string{"status", "--porcelain"}, []byte(" M file.txt\n"))

    g := New(r)
    dirty, err := g.IsDirty(context.Background())
    if err != nil {
        t.Fatalf("err: %v", err)
    }
    if !dirty {
        t.Fatalf("expected dirty")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL undefined

**Step 3: Write minimal implementation**

Implement:
- `AddAll(ctx)` => `git add .`
- `IsDirty(ctx)` => `git status --porcelain` non-empty
- `Commit(ctx,msg)` => `git commit -m <msg>`
- `RevParseHead(ctx)` => `git rev-parse HEAD`

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/vcs/git/git.go internal/vcs/git/git_test.go
git commit -m "feat: add git CLI adapter"
```

### Task 8: Implement runner orchestration (run once)

**Files:**
- Create: `internal/runner/runner.go`
- Create: `internal/runner/runner_test.go`
- Modify: `cmd/yolo-runner/main.go`

**Step 1: Write the failing test**

Create `internal/runner/runner_test.go` with a full happy-path simulation using fakes:
- beads fake returns leaf task
- show returns title/desc/acceptance
- opencode fake "runs" and writes log path
- git fake returns dirty => commit path taken

Example (pseudo-level, but implement fully):

```go
func TestRunOnce_CommitsAndCloses(t *testing.T) {
    // Arrange fakes and canned outputs
    // Act: RunOnce(...)
    // Assert: expected bead status updates, git commit called, bd close called, bd sync called
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL undefined `RunOnce`

**Step 3: Write minimal implementation**

Implement `RunOnce(ctx, deps, cfg) (string, error)` that returns result string like python:
- `"no_tasks"`, `"dry_run"`, `"blocked"`, `"completed"`

Orchestration exact sequence (v1):
1. leaf := SelectFirstOpenLeafTaskID
2. if none => no_tasks
3. show => build prompt
4. if dry_run => print and return dry_run
5. bd update status in_progress
6. run opencode and write per-task log
7. git add .
8. if git clean => log blocked + bd update blocked
9. else git commit with `feat: <lower(title)>` fallback `feat: complete bead task`
10. rev-parse HEAD => write completed log
11. bd close
12. bd show => verify status == closed else mark blocked
13. bd sync

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go cmd/yolo-runner/main.go
git commit -m "feat: run single bead task with opencode"
```

### Task 9: Implement loop and max-tasks

**Files:**
- Modify: `internal/runner/runner.go`
- Create: `internal/runner/loop_test.go`

**Step 1: Write the failing test**

```go
func TestRunLoop_StopsAfterMax(t *testing.T) {
    // Arrange RunOnce stub that always returns completed
    // Expect RunOnce called max times
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL

**Step 3: Write minimal implementation**

Add `RunLoop(ctx, deps, cfg) (completed int, err error)` matching python semantics.

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/runner/runner.go internal/runner/loop_test.go
git commit -m "feat: add run loop with max tasks"
```

### Task 10: Add build/test docs and a make target

**Files:**
- Create: `Makefile`
- Modify: `README.md`

**Step 1: Write the failing test**

No code test; instead add a `make test` and `make build` and verify locally.

**Step 2: Run build/test to verify it works**

Run:
- `make test`
- `make build`

Expected:
- tests PASS
- binary produced at `./bin/yolo-runner` (or similar)

**Step 3: Minimal implementation**

Makefile targets:
- `test: go test ./...`
- `build: go build -o bin/yolo-runner ./cmd/yolo-runner`

Update README to describe:
- prerequisites (bd/opencode/git)
- usage examples including `--model`

**Step 4: Commit**

```bash
git add Makefile README.md
git commit -m "docs: add Go build and usage"
```

## v2/v3 Beads Tasks (create issues)

Create these as separate beads issues (do not implement in v1):

### v2
- Abstract task tracker integration (Beads adapter behind interface)
- Abstract coding agent integration (OpenCode adapter behind interface)
- Add web server that serves progress + current task + recent events/log tails

### v3
- Abstract VCS integration (git adapter behind interface; support alternatives)

## Verification Checklist (before calling v1 complete)

Run in repo root (worktree):
- `go test ./...`
- `make build`
- `./bin/yolo-runner --dry-run --repo . --root <some-root>`

If running real mode in this repo:
- Ensure a safe test bead exists; expect it to modify files and commit.

