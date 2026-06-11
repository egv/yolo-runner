package preflight

import (
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestBuildPromptIncludesReadOnlyInstructionsAndSchemaSnapshot(t *testing.T) {
	prompt := BuildPrompt(BuildPromptInput{
		Task: contracts.Task{
			ID:          "task-123",
			Title:       "Add retry guard",
			Description: "Wire the retry guard into the agent loop.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "epic-1",
			Metadata:    map[string]string{"dependencies": "T17, T18"},
		},
		Comments: []Comment{
			{Author: "alice", Body: "Keep the change in internal/agent."},
			{Author: "bob", Body: "The result parser landed in T17."},
		},
		QueueRoot: contracts.Task{
			ID:          "epic-1",
			Title:       "Agent preflight",
			Description: "Preflight checks for queued tasks.",
			Status:      contracts.TaskStatusOpen,
		},
	})

	want := `You are evaluating whether a queued task is actionable before an implementation agent runs.

Rules:
- Read only. Do not edit, create, delete, rename, format, or stage files.
- Do not update task status, add comments, commit, push, or start an implementation.
- You may read files in the workspace to answer your own questions before deciding.
- Check relevant workspace files before asking a human, especially outputs of dependency tasks.
- Decide whether the implementation agent can proceed without asking a human for missing information.

Task:
ID: task-123
Title: Add retry guard
Status: open
Parent ID: epic-1

Description:
Wire the retry guard into the agent loop.

Comments:
1. alice: Keep the change in internal/agent.
2. bob: The result parser landed in T17.

Dependency hints:
- T17
- T18

Queue root:
ID: epic-1
Title: Agent preflight
Status: open

Description:
Preflight checks for queued tasks.

Required JSON response:
Return only valid JSON matching this schema:
{
  "decision": "ready | needs_info | reply",
  "confidence": 0.0,
  "summary": "one concise sentence explaining the decision",
  "questions": ["specific question for missing information"],
  "reply_text": "answer to the newest human question"
}

Schema rules:
- decision must be "ready" only when the task is actionable from the provided context.
- decision must be "needs_info" when scope, expected behavior, ownership, or acceptance criteria are unclear.
- decision must be "reply" when the newest human comment asks a question about the existing needs-info request instead of providing the requested implementation details.
- confidence must be a number from 0 to 1. Use ready only when confidence is at least 0.80.
- questions must be empty for ready and reply, and contain concrete questions for needs_info.
- reply_text must be empty for ready and needs_info, and contain a direct answer for reply. Do not repeat the questionnaire in reply_text.
- Write summary, questions, and reply_text in the same natural language as the task description and recent human comments. If languages differ, prefer the most recent human comment language.

Question: Is this task actionable for an implementation agent?`

	if prompt != want {
		t.Fatalf("BuildPrompt() snapshot mismatch\nwant:\n%s\n\ngot:\n%s", want, prompt)
	}
}
