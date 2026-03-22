---
description: Split a spec into very small strict-TDD micro-tasks
agent: plan
---
Load the `task-splitting` skill and perform an aggressive micro-splitting pass.

Input:
$ARGUMENTS

Constraints:
- prefer the smallest reasonable slice
- one seam per task
- one strict red-green loop per task
- no mixed implementation, docs, integration, or e2e work
- explicit out-of-scope for every task
- explicit dependency chain so only the next intended task is ready
