# M2 Integration Contract Test (Spec)

Codename: BENDER
Owner repo: bender-graph-runner

This directory is the authoritative specification for the M2 integration contract between:
- `bender-task-manager` (taskd)
- `bender-mastermind` (mastermindd + runnerd)

Goals:
- Catch RPC and semantic drift early.
- Provide a repeatable end-to-end demo.

## Fixture
- `bender-graph-runner/contract-tests/m2/fixture.yaml`

The fixture describes a small task graph with:
- A valid task (should be executed and closed)
- A dependent task (blocked until the first closes)
- An ambiguous task (missing acceptance criteria; should be blocked + commented)

## Expected Outcomes
- Valid task ends `closed`.
- Dependent task ends `closed`, and only becomes ready after the dependency closes.
- Ambiguous task ends `blocked` with block reason `needs_info`, and receives a clarification comment.

Status vocabulary for this fixture:
- `open`, `blocked`, `in_progress`, and `closed` are task statuses.
- `needs_info` is a block reason attached to `blocked`.

## Harness (Implementation TBD)
The contract test runner should:
1. Start `taskd` with a fresh SQLite DB.
2. Load the fixture into Task Manager (via RPC or CLI).
3. Start `runnerd` with a stub adapter (always succeeds).
4. Start `mastermindd` and point it at taskd and runnerd.
5. Wait for the expected final states (with a timeout).
6. Save an NDJSON event log for debugging.

Constraints:
- No network calls.
- Runs in CI in under ~30s.
