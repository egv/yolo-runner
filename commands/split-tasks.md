---
description: Split a spec, ADR, or PRD into agent-ready epics and tasks
agent: plan
---
Split the following into agent-ready epics and tasks using this command text directly.

Do not run shell commands, inspect files, or search the filesystem for extra commands,
skills, docs, or repository context. If context is missing, record the gap in Risk notes
instead of looking it up.

Input:
$ARGUMENTS

Requirements:
- produce explicit dependency order
- include strict TDD acceptance criteria
- call out any tasks that are still too broad

Each task should include:
- Title
- Why
- In scope
- Out of scope
- Strict TDD steps
- Done when
- Expected files
- Depends on
- Unlocks

Output structure:
- `## Epics`
- `## Tasks` containing only summary list items in the exact form `- <task id>: <title>`
- `## Order`
- `## Risk notes`
- after `## Risk notes`, one full task section for every task using `### Task: <task id> <title>` headings

Do not place full task sections inside the `## Tasks` summary section.
