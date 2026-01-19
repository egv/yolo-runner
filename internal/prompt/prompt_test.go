package prompt

import "testing"

func TestBuildIncludesAllFields(t *testing.T) {
	prompt := Build("task-1", "Title", "Desc", "Acceptance")

	if !containsAll(prompt, []string{
		"You are in YOLO mode",
		"Your task is: task-1 - Title",
		"**Description:**",
		"Desc",
		"**Acceptance Criteria:**",
		"Acceptance",
		"**Strict TDD Protocol:**",
		"**Rules:**",
		"Start now by analyzing the codebase and writing your first failing test.",
	}) {
		t.Fatalf("prompt missing expected content: %q", prompt)
	}
}

func containsAll(text string, parts []string) bool {
	for _, part := range parts {
		if !contains(text, part) {
			return false
		}
	}
	return true
}

func contains(text string, part string) bool {
	return len(part) == 0 || (len(text) >= len(part) && (index(text, part) >= 0))
}

func index(text string, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
