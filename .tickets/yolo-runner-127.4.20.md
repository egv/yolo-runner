---
id: yolo-runner-127.4.20
status: closed
deps: [yolo-runner-127.4.19]
links: []
created: 2026-01-26T22:03:05.277615+03:00
type: bug
priority: 2
parent: yolo-runner-127.4
---
# Bug: progress counter exceeds total

## Problem\nProgress counter can exceed total (e.g., [4/3]).\n\n## Repro\nRun runner on root with 3 leaf tasks and observe progress display after multiple updates.\n\n## Expected\n- x never exceeds y\n- counter reflects completed/total runnable leaves\n\n## Suspected area\nProgress state increment/update logic in runner/ UI statusbar.\n

## Acceptance Criteria

- Progress [x/y] never exceeds y\n- x increments once per completed/blocked leaf\n- y is total runnable leaves under root\n- Add regression test covering over-increment scenario\n- go test ./... passes


