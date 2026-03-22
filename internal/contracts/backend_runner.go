package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type BackendErrorClassifier func(error) bool

func NormalizeBackendRunnerResult(startedAt time.Time, finishedAt time.Time, request RunnerRequest, runErr error, isBlocked BackendErrorClassifier) RunnerResult {
	result := RunnerResult{
		LogPath:    "",
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	if runErr == nil {
		result.Status = RunnerResultCompleted
		return result
	}
	if isBlocked != nil && isBlocked(runErr) {
		result.Status = RunnerResultBlocked
		result.Reason = runErr.Error()
		return result
	}
	if request.Timeout > 0 && errors.Is(runErr, context.DeadlineExceeded) {
		result.Status = RunnerResultBlocked
		result.Reason = timeoutResultReason(request.Timeout, runErr)
		return result
	}
	result.Status = RunnerResultFailed
	result.Reason = runErr.Error()
	return result
}

func timeoutResultReason(timeout time.Duration, runErr error) string {
	reason := fmt.Sprintf("runner timeout after %s", timeout)
	if runErr == nil {
		return reason
	}
	detail := strings.TrimSpace(runErr.Error())
	if detail == "" || detail == context.DeadlineExceeded.Error() {
		return reason
	}
	return reason + ": " + detail
}
