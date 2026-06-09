package arcreview

import "strings"

type PRRunnerAction string

const (
	PRRunnerActionWait   PRRunnerAction = "wait"
	PRRunnerActionReview PRRunnerAction = "review"
	PRRunnerActionAnswer PRRunnerAction = "answer"
	PRRunnerActionShip   PRRunnerAction = "ship"
)

func PlanNextPRRunnerAction(state PRRuntimeState, handledRevision string, allowShip bool) PRRunnerAction {
	if isTerminalPRStatus(state.Details.Status) {
		return PRRunnerActionWait
	}
	if hasNewPRRevision(state, handledRevision) {
		return PRRunnerActionReview
	}
	if hasUnansweredPRComments(state.Comments) {
		return PRRunnerActionAnswer
	}
	if hasOpenPRBlockers(state.OpenIssues) {
		return PRRunnerActionReview
	}
	if hasFailedPRChecks(state.Checks) {
		return PRRunnerActionReview
	}
	if hasPendingPRChecks(state.Checks) || hasUnknownPRChecks(state.Checks) {
		return PRRunnerActionWait
	}
	if allowShip {
		return PRRunnerActionShip
	}
	return PRRunnerActionWait
}

func isTerminalPRStatus(status string) bool {
	switch normalizedPRStatus(status) {
	case "abandoned", "cancelled", "canceled", "closed", "completed", "declined", "landed", "merged", "rejected":
		return true
	default:
		return false
	}
}

func hasNewPRRevision(state PRRuntimeState, handledRevision string) bool {
	currentRevision := currentPRRevision(state)
	if currentRevision == "" {
		return false
	}
	return currentRevision != strings.TrimSpace(handledRevision)
}

func currentPRRevision(state PRRuntimeState) string {
	if revision := strings.TrimSpace(state.Revision); revision != "" {
		return revision
	}
	return strings.TrimSpace(state.Details.Revision)
}

func hasUnansweredPRComments(comments []PRComment) bool {
	for _, comment := range comments {
		if !comment.Resolved && !comment.Answered {
			return true
		}
	}
	return false
}

func hasOpenPRBlockers(issues []PRIssue) bool {
	for _, issue := range issues {
		if isOpenIssue(issue) {
			return true
		}
	}
	return false
}

func hasPendingPRChecks(checks []PRCheck) bool {
	for _, check := range checks {
		if isPendingPRCheckStatus(check.Status) {
			return true
		}
	}
	return false
}

func hasFailedPRChecks(checks []PRCheck) bool {
	for _, check := range checks {
		if isFailedPRCheckStatus(check.Status) {
			return true
		}
	}
	return false
}

func hasUnknownPRChecks(checks []PRCheck) bool {
	for _, check := range checks {
		if !isKnownPRCheckStatus(check.Status) {
			return true
		}
	}
	return false
}

func isKnownPRCheckStatus(status string) bool {
	return isSuccessfulPRCheckStatus(status) || isPendingPRCheckStatus(status) || isFailedPRCheckStatus(status)
}

func isSuccessfulPRCheckStatus(status string) bool {
	switch normalizedPRStatus(status) {
	case "green", "neutral", "ok", "pass", "passed", "skipped", "success", "successful", "succeeded":
		return true
	default:
		return false
	}
}

func isPendingPRCheckStatus(status string) bool {
	switch normalizedPRStatus(status) {
	case "created", "in-progress", "in_progress", "pending", "queued", "running", "scheduled", "waiting":
		return true
	default:
		return false
	}
}

func isFailedPRCheckStatus(status string) bool {
	switch normalizedPRStatus(status) {
	case "cancelled", "canceled", "error", "errored", "failed", "failure", "red", "timed-out", "timed_out":
		return true
	default:
		return false
	}
}

func normalizedPRStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}
