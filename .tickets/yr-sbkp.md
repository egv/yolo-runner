---
id: yr-sbkp
status: open
deps: []
links: []
created: 2026-02-10T08:16:58Z
type: epic
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-GUI Elm-style TUI architecture and production UX

Implement production-grade stdin-driven TUI architecture using Elm-like state/update/view model. State is derived (no raw event storage), hierarchy is Run->Workers->Tasks, support expand/collapse navigation, rich status bar metrics, and scale baseline of 10 workers/500 tasks.


## Notes

**2026-02-10T08:19:52Z**

Implementation protocol (MANDATORY): STRICT TDD for every subtask: (1) write failing test first, (2) implement minimal code to pass, (3) refactor with tests green. No feature code before a failing test exists.

**2026-02-10T08:19:52Z**

UI stack requirement (MANDATORY): Bubble Tea + Bubbles + Lip Gloss. Use Elm-style architecture (Model/Msg/Update/View), with state derived from stdin NDJSON events (no raw event log state).

**2026-02-10T08:25:05Z**

Critical requirement: design error-state handling from the start across all layers (stream ingest, decode/schema, reducer/state transition, worker/task/runner derived state, render/view, interaction/input, and backend/landing/tracker integration). Error states must be first-class, test-driven, and visible in UI at appropriate scope.
