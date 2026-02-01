---
id: yolo-runner-7ld.3
status: open
deps: []
links: []
created: 2026-01-18T21:39:39.108046+03:00
type: task
priority: 2
parent: yolo-runner-7ld
---
# v2: OpenCode adapter implements CodingAgent

Implement OpenCode-backed CodingAgent using opencode CLI, including model forwarding and non-interactive env.

## Acceptance Criteria

- Given a repo root and prompt, when CodingAgent.Run(prompt, model) is called, then it shells out to OpenCode with `--agent yolo --format json` and optional `--model <id>`
- Given a log output path, when Run is called, then stdout is captured to that path (overwriting per run)
- Given non-interactive requirements, when Run is called, then it sets the same env vars as v1 (CI=true and OPENCODE_DISABLE_* flags)
- Given unit tests, when run, then they verify command args and env construction


