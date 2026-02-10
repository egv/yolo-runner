---
id: yr-tilv
status: open
deps: [yr-a0vs]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T8 Emit command lifecycle and output events from agent

STRICT TDD: failing tests first. Emit runner_cmd_started/finished, runner_output, and runner_warning events with bounded payloads and redaction.

## Acceptance Criteria

Given task execution, when commands run, then event stream includes start/finish metadata and recent output snippets in near real time.

