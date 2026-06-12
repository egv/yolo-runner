package executor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func BuildPrompt(task contracts.Task, mode contracts.RunnerMode, tddMode bool) string {
	modeLine := "Implementation"
	if mode == contracts.RunnerModeReview {
		modeLine = "Review"
	}
	sections := []string{
		"Mode: " + modeLine,
		"Task ID: " + task.ID,
		"Title: " + task.Title,
	}
	if mode == contracts.RunnerModeReview {
		sections = append(sections, strings.Join([]string{
			"Review Instructions:",
			"- Include exactly one verdict line in this format: REVIEW_VERDICT: pass OR REVIEW_VERDICT: fail",
			"- Use pass only when implementation satisfies acceptance criteria and tests.",
			"- If fail, include exactly one structured line: REVIEW_FAIL_FEEDBACK: <blocking gaps and required fixes>.",
		}, "\n"))
	} else {
		sections = append(sections, strings.Join([]string{
			"Command Contract:",
			"- Work only on this task; do not switch tasks.",
			"- Do not call task-selection/status tools (the runner owns task state).",
			"- Keep edits scoped to files required for this task.",
		}, "\n"))
		if tddMode {
			sections = append(sections, strings.Join([]string{
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
			}, "\n"))
		} else {
			sections = append(sections, strings.Join([]string{
				"Strict TDD Checklist:",
				"[ ] Add or update a test that fails for the target behavior.",
				"[ ] Run the targeted test and confirm it fails before implementation.",
				"[ ] Implement the minimal code change required for the test to pass.",
				"[ ] Re-run targeted tests, then run broader relevant tests.",
				"[ ] Stop only when all tests pass and acceptance criteria are covered.",
			}, "\n"))
		}
		if retryAttempt, blockers := reviewRetryPromptContext(task.Metadata); retryAttempt > 0 {
			retrySection := []string{
				"Retry Context:",
				fmt.Sprintf("- Review retry attempt: %d", retryAttempt),
				"Prior Review Blockers:",
			}
			if blockers != "" {
				retrySection = append(retrySection, "- "+blockers)
			} else {
				retrySection = append(retrySection, "- Previous review failed; address blockers before requesting review again.")
			}
			sections = append(sections, strings.Join(retrySection, "\n"))
		}
	}
	if strings.TrimSpace(task.Description) != "" {
		sections = append(sections, "Description:\n"+task.Description)
	}
	return strings.Join(sections, "\n\n")
}

func BuildImplementPrompt(task contracts.Task, reviewFeedback string, reviewRetryCount int, completionFeedback string, completionRetryCount int, tddMode bool) string {
	prompt := BuildPrompt(task, contracts.RunnerModeImplement, tddMode)
	feedback := strings.TrimSpace(reviewFeedback)
	if feedback != "" && reviewRetryCount > 0 {
		prompt = strings.Join([]string{
			prompt,
			strings.Join([]string{
				fmt.Sprintf("Review Remediation Loop: Attempt %d", reviewRetryCount),
				"A previous review run failed. Address all blocking review comments before requesting review again.",
				"REVIEW_FAIL_FEEDBACK:",
				feedback,
			}, "\n"),
		}, "\n\n")
	}

	completionFeedback = strings.TrimSpace(completionFeedback)
	if completionFeedback != "" && completionRetryCount > 0 {
		prompt = strings.Join([]string{
			prompt,
			strings.Join([]string{
				fmt.Sprintf("Completion Remediation Loop: Attempt %d", completionRetryCount),
				"REMEDIATION_ADDENDUM:",
				completionFeedback,
			}, "\n"),
		}, "\n\n")
	}
	return prompt
}

func BuildReviewVerdictPrompt(task contracts.Task) string {
	sections := []string{
		"Mode: Review",
		"Task ID: " + task.ID,
		"Title: " + task.Title,
		"Verdict-only follow-up:",
		"- Your previous review did not include the required structured verdict.",
		"- Respond with exactly one line and no extra text:",
		"REVIEW_VERDICT: pass",
		"or",
		"REVIEW_VERDICT: fail",
	}
	return strings.Join(sections, "\n")
}

func ReviewRetryBlockersFromMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	for _, key := range []string{"review_fail_feedback", "review_feedback", "triage_reason"} {
		if blocker := strings.TrimSpace(metadata[key]); blocker != "" {
			return blocker
		}
	}
	return ""
}

func reviewRetryPromptContext(metadata map[string]string) (int, string) {
	if len(metadata) == 0 {
		return 0, ""
	}
	retryAttempt, err := strconv.Atoi(strings.TrimSpace(metadata["review_retry_count"]))
	if err != nil || retryAttempt <= 0 {
		return 0, ""
	}
	return retryAttempt, ReviewRetryBlockersFromMetadata(metadata)
}
