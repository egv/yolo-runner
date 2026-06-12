package agent

import (
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func mapResultToTaskUpdate(result workitem.ImplementResult) (contracts.TaskStatus, map[string]string) {
	switch contracts.RunnerResultStatus(strings.TrimSpace(result.Status)) {
	case contracts.RunnerResultCompleted:
		return contracts.TaskStatusClosed, nil
	case contracts.RunnerResultBlocked:
		return contracts.TaskStatusBlocked, taskDataForTerminalResult("blocked", result)
	case contracts.RunnerResultFailed:
		return contracts.TaskStatusFailed, taskDataForTerminalResult("failed", result)
	default:
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = fmt.Sprintf("invalid implement result status %q", result.Status)
		}
		result.Status = string(contracts.RunnerResultFailed)
		result.Reason = reason
		return contracts.TaskStatusFailed, taskDataForTerminalResult("failed", result)
	}
}

func taskDataForTerminalResult(triageStatus string, result workitem.ImplementResult) map[string]string {
	triageStatus = strings.TrimSpace(triageStatus)
	if triageStatus == "" {
		return nil
	}

	data := map[string]string{"triage_status": triageStatus}
	reason := firstNonEmptyString(result.Reason, result.Artifacts["triage_reason"], result.Artifacts["reason"])
	if reason != "" {
		data["triage_reason"] = reason
		data["reason"] = reason
	}
	data["decision"] = triageStatus

	appendResultReviewOutcome(data, result)
	switch triageStatus {
	case "blocked":
		copyResultArtifact(data, result, "completion_retry_count")
		copyResultArtifact(data, result, "completion_addendum")
		copyResultArtifact(data, result, "landing_status")
		copyResultArtifact(data, result, "auto_commit_sha")
	case "failed":
		copyResultArtifact(data, result, "review_retry_count")
	}

	return data
}

func appendResultReviewOutcome(data map[string]string, result workitem.ImplementResult) {
	verdict := strings.ToLower(firstNonEmptyString(result.ReviewVerdict, result.Artifacts["review_verdict"]))
	if verdict == "pass" || verdict == "fail" {
		data["review_verdict"] = verdict
	}
	if feedback := firstNonEmptyString(result.Artifacts["review_fail_feedback"], result.Artifacts["review_feedback"]); feedback != "" {
		data["review_fail_feedback"] = feedback
	}
}

func copyResultArtifact(data map[string]string, result workitem.ImplementResult, key string) {
	value := strings.TrimSpace(result.Artifacts[key])
	if value == "" {
		return
	}
	data[key] = value
}
