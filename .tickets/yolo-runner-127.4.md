---
id: yolo-runner-127.4
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.038426+03:00
type: epic
priority: 1
parent: yolo-runner-127
---
# v1.1: Observability (TUI + watchdog + init/guards)

Make the runner robust and observable before adding molecule traversal.

Includes:
- Bubble Tea TUI progress UI (TTY by default, with --headless)
- Watchdog to detect and fail-fast on OpenCode stalls, with evidence
- Init/guards to ensure YOLO agent is installed and permissions allow non-interactive runs
- Fix default --root behavior (avoid hardcoded algi-8bt)

All implementation must be Go.

## Acceptance Criteria

- Runner does not hang indefinitely on OpenCode stalls (watchdog)
- Runner has clear progress output (TUI on TTY, --headless for logs)
- Runner refuses to start if YOLO agent missing; init installs it
- Default root is inferred or runner errors clearly
- go test ./... passes


