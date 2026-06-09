package preflight

import (
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type BuildPromptInput struct {
	Task      contracts.Task
	Comments  []Comment
	QueueRoot contracts.Task
}

type Comment struct {
	Author string
	Body   string
}

func BuildPrompt(input BuildPromptInput) string {
	return strings.Join([]string{
		"You are evaluating whether a queued task is actionable before an implementation agent runs.",
		readOnlyRules(),
		taskSection(input.Task),
		commentsSection(input.Comments),
		queueRootSection(input.QueueRoot),
		responseSchemaSection(),
	}, "\n\n")
}

func readOnlyRules() string {
	return strings.Join([]string{
		"Rules:",
		"- Read only. Do not edit, create, delete, rename, format, or stage files.",
		"- Do not update task status, add comments, commit, push, or start an implementation.",
		"- Use only the task, comments, and queue root context below.",
		"- Decide whether the implementation agent can proceed without asking a human for missing information.",
	}, "\n")
}

func taskSection(task contracts.Task) string {
	return strings.Join([]string{
		"Task:",
		"ID: " + fallback(task.ID),
		"Title: " + fallback(task.Title),
		"Status: " + fallback(string(task.Status)),
		"Parent ID: " + fallback(task.ParentID),
		"",
		"Description:",
		fallback(task.Description),
	}, "\n")
}

func commentsSection(comments []Comment) string {
	lines := []string{"Comments:"}
	for _, comment := range comments {
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s: %s", len(lines), fallback(comment.Author), body))
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func queueRootSection(root contracts.Task) string {
	return strings.Join([]string{
		"Queue root:",
		"ID: " + fallback(root.ID),
		"Title: " + fallback(root.Title),
		"Status: " + fallback(string(root.Status)),
		"",
		"Description:",
		fallback(root.Description),
	}, "\n")
}

func responseSchemaSection() string {
	return `Required JSON response:
Return only valid JSON matching this schema:
{
  "decision": "ready | needs_info",
  "confidence": 0.0,
  "summary": "one concise sentence explaining the decision",
  "questions": ["specific question for missing information"]
}

Schema rules:
- decision must be "ready" only when the task is actionable from the provided context.
- decision must be "needs_info" when scope, expected behavior, ownership, or acceptance criteria are unclear.
- confidence must be a number from 0 to 1. Use ready only when confidence is at least 0.80.
- questions must be empty for ready and contain concrete questions for needs_info.
- Write summary and questions in the same natural language as the task description and recent human comments. If languages differ, prefer the most recent human comment language.

Question: Is this task actionable for an implementation agent?`
}

func fallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "None"
	}
	return value
}
