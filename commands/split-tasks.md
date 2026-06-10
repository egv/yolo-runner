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
- return strict JSON only, with arrays for all repeated fields

Return only valid JSON. Do not wrap it in markdown or code fences. Use exactly
these top-level fields:

```json
{
  "epics": [{"name": "<epic name>", "goal": "<goal>"}],
  "tasks": [
    {
      "id": "<task id>",
      "title": "<title>",
      "why": ["<one sentence>"],
      "in_scope": ["<specific behavior>", "<specific seam>"],
      "out_of_scope": ["<explicit exclusions>"],
      "strict_tdd": [
        "Add or update one targeted failing test first",
        "Run the targeted test and confirm it fails for the intended reason",
        "Implement the minimum production change needed to make it pass",
        "Re-run the targeted test",
        "Run one narrow follow-up verification command"
      ],
      "done_when": ["<specific test or command passes>", "<specific behavior is verified>"],
      "expected_files": ["<prod files>", "<test files>"],
      "depends_on": ["none"],
      "unlocks": ["<task id>"]
    }
  ],
  "order": [{"from": "<task id>", "to": "<task id>"}],
  "risk_notes": ["<risk or missing context>"]
}
```

Use `order: []` when there are no dependency edges. Use `risk_notes: ["none"]`
only when there are no known risks or missing-context notes. Do not include any
markdown headings, task templates, comments, trailing prose, or fields not shown
above.
