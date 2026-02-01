---
id: yolo-runner-127.6.2
status: in_progress
deps: []
links: []
created: 2026-01-20T12:43:38.465761+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Console output: avoid printing full issue description

## Problem
When running `bin/yolo-runner`, the console output can print the full issue description (large prompt-like text). This is noisy and makes it hard to see progress.

## Expected
Console output should stay compact:
- show issue id + title
- show short phase/state lines
- avoid dumping full `description` / `acceptance_criteria` content to stdout

## Notes / suspected area
Likely coming from:
- the OpenCode command invocation echoing the *entire prompt* as an argument (current BuildArgs uses `opencode run <prompt> ...`)
- new command echoing (`$ ...`) printing the full arg list

## Acceptance
- External command echoing prints a compact version for OpenCode (e.g. `$ opencode run <prompt redacted> --agent yolo --format json .`)
- No other code path prints issue description/acceptance criteria verbatim
- `go test ./...` passes



