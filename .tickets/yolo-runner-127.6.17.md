---
id: yolo-runner-127.6.17
status: in_progress
deps: []
links: []
created: 2026-01-24T16:37:41.351515+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Runner output shifts and overlaps command lines

Runner output is visually shifted/overlapping with command lines, making the TUI/plain output unreadable. Example output when running ./yolo-runner --root yolo-runner-127.6 (output alignment jumps right and overlaps previous lines, e.g. task title and phase appear offset).

Observed:
- bd command lines and runner status lines overlap and shift horizontally
- Output shows duplicated/shifted text like "llast output" and multiple lines aligned far right

Expected:
- Runner output lines should be aligned consistently with no overlap or horizontal shifts.


