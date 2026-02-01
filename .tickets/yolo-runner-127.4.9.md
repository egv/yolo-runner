---
id: yolo-runner-127.4.9
status: closed
deps: []
links: []
created: 2026-01-19T18:40:03.684668+03:00
type: epic
priority: 0
parent: yolo-runner-127.4
---
# v1.1: Align Go runner OpenCode invocation with Python runner

The Python beads runner is more reliable than the Go runner because it invokes OpenCode differently:

1) Config isolation:
- Python sets XDG_CONFIG_HOME to ~/.config/opencode-runner and forces OPENCODE_CONFIG_DIR/OPENCODE_CONFIG/OPENCODE_CONFIG_CONTENT.
- Go runner currently leaves configRoot/configDir empty, so OpenCode uses the user global config, which can trigger interactive questions/permissions and hangs.

2) Log integrity:
- Python redirects only stdout to runner-logs/opencode/<issue>.jsonl.
- Go runner currently redirects BOTH stdout and stderr into the JSONL file, which can corrupt the JSONL stream and break log-watcher/spinner logic.

This epic aligns Go behavior to match the proven Python behavior, reducing hangs and making watchdog/logging reliable.

## Acceptance Criteria

- Go runner sets isolated OpenCode config by default (same as Python runner) unless user overrides
- Go runner writes OpenCode stdout to runner-logs/opencode/<issue>.jsonl and does NOT mix stderr into that file
- Changes are covered by unit tests
- go test ./... passes


