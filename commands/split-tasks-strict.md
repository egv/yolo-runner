---
description: Split a spec into very small strict-TDD micro-tasks
agent: plan
---
Perform an aggressive strict-TDD micro-splitting pass using this command text directly.

Do not run shell commands, inspect files, or search the filesystem for extra commands,
skills, docs, or repository context. If context is missing, record the gap in Risk notes
instead of looking it up.

Input:
$ARGUMENTS

Constraints:
- prefer the smallest reasonable slice
- one seam per task
- one strict red-green loop per task
- no mixed implementation, docs, integration, or e2e work
- explicit out-of-scope for every task
- explicit dependency chain so only the next intended task is ready

Required task template for every task:

### Task: <task id> <title>

Why:
- <one sentence>

In scope:
- <specific behavior>
- <specific seam>

Out of scope:
- <explicit exclusions>

Strict TDD:
1. Add or update one targeted failing test first
2. Run the targeted test and confirm it fails for the intended reason
3. Implement the minimum production change needed to make it pass
4. Re-run the targeted test
5. Run one narrow follow-up verification command

Done when:
- <specific test or command passes>
- <specific behavior is verified>

Expected files:
- <prod files>
- <test files>

Depends on:
- <task IDs or none>

Unlocks:
- <task IDs or none>

Required output structure:
- `## Epics`
- `## Tasks` containing only summary list items in the exact form `- <task id>: <title>`
- `## Order`
- `## Risk notes`
- after `## Risk notes`, one full strict task template for every task using `### Task: <task id> <title>` headings

Do not place full task templates inside the `## Tasks` summary section.
