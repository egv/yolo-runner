package contracts

import (
	"context"
	"fmt"
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
	if request.Timeout > 0 && runErr == context.DeadlineExceeded {
		result.Status = RunnerResultBlocked
		result.Reason = fmt.Sprintf("runner timeout after %s", request.Timeout)
		return result
	}
	result.Status = RunnerResultFailed
	result.Reason = runErr.Error()
	return result
}
