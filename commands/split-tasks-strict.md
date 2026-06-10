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
- all output arrays must be JSON arrays, never comma-delimited prose
- write generated epic names/goals, task titles, task details, and risk notes
  in the same human language as the input; preserve product names, identifiers,
  paths, commands, labels, and API names verbatim

Return only valid JSON. Do not wrap it in markdown or code fences. Use exactly this
top-level shape:

```json
{
  "epics": [
    {"name": "<epic name>", "goal": "<goal>"}
  ],
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
  "order": [
    {"from": "<task id>", "to": "<task id>"}
  ],
  "risk_notes": ["<risk or missing context>"]
}
```

Use `order: []` when there are no dependency edges. Use `risk_notes: ["none"]`
only when there are no known risks or missing-context notes. Do not include any
markdown headings, task templates, comments, trailing prose, or fields not shown
above.
