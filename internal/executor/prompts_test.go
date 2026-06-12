package executor

import (
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestBuildPromptReviewRequiresStructuredVerdict(t *testing.T) {
	prompt := BuildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Check behavior"}, contracts.RunnerModeReview, false)
	if !strings.Contains(prompt, "Mode: Review") {
		t.Fatalf("expected review mode marker in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_VERDICT: pass") || !strings.Contains(prompt, "REVIEW_VERDICT: fail") {
		t.Fatalf("expected structured review verdict instructions, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_FAIL_FEEDBACK:") {
		t.Fatalf("expected structured review fail feedback instructions, got %q", prompt)
	}
}

func TestBuildPromptImplementExcludesReviewVerdictInstructions(t *testing.T) {
	prompt := BuildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, false)
	if !strings.Contains(prompt, "Mode: Implementation") {
		t.Fatalf("expected implementation mode marker in prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "REVIEW_VERDICT") {
		t.Fatalf("did not expect review verdict instructions in implement prompt, got %q", prompt)
	}
}

func TestBuildPromptImplementIncludesCommandContractAndTDDChecklist(t *testing.T) {
	prompt := BuildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, false)

	required := []string{
		"Command Contract:",
		"- Work only on this task; do not switch tasks.",
		"- Do not call task-selection/status tools (the runner owns task state).",
		"- Keep edits scoped to files required for this task.",
		"Strict TDD Checklist:",
		"[ ] Add or update a test that fails for the target behavior.",
		"[ ] Run the targeted test and confirm it fails before implementation.",
		"[ ] Implement the minimal code change required for the test to pass.",
		"[ ] Re-run targeted tests, then run broader relevant tests.",
		"[ ] Stop only when all tests pass and acceptance criteria are covered.",
	}
	for _, needle := range required {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to include %q, got %q", needle, prompt)
		}
	}
}

func TestBuildPromptImplementIncludesRedGreenRefactorWorkflowWhenTDDModeEnabled(t *testing.T) {
	prompt := BuildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, true)

	required := []string{
		"Strict TDD Workflow (Red-Green-Refactor):",
		"Tests-First Gate:",
		"- Confirm tests for the target behavior exist before implementation.",
		"- Run tests before changes and confirm they fail to define expected behavior.",
		"- Do not implement until tests-first gate is passing.",
		"1. RED: Add or update a test that fails for the target behavior.",
		"2. GREEN: Implement the minimal code required for that test to pass.",
		"3. REFACTOR: Improve the design while preserving passing tests.",
		"- Required sequence: test-first, targeted fail check, minimal green fix, then refactor.",
		"- Re-run targeted tests, then run broader relevant tests.",
		"- Stop only when all tests pass and acceptance criteria are covered.",
	}
	for _, needle := range required {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to include %q, got %q", needle, prompt)
		}
	}
}

func TestBuildImplementPromptIncludesReviewFeedbackWhenRetrying(t *testing.T) {
	prompt := BuildImplementPrompt(
		contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"},
		"add RED/GREEN note evidence to ticket",
		1,
		"",
		0,
		false,
	)

	if !strings.Contains(prompt, "Review Remediation Loop: Attempt 1") {
		t.Fatalf("expected remediation loop attempt marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_FAIL_FEEDBACK:") {
		t.Fatalf("expected structured review feedback marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "add RED/GREEN note evidence to ticket") {
		t.Fatalf("expected review feedback body in prompt, got %q", prompt)
	}
}
