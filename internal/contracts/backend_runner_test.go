package contracts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBackendRunnerResult(t *testing.T) {
	started := time.Date(2026, 2, 10, 8, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)

	blockedSentinel := errors.New("blocked")
	tests := []struct {
		name      string
		request   RunnerRequest
		runErr    error
		isBlocked BackendErrorClassifier
		want      RunnerResultStatus
		contains  string
	}{
		{name: "implement success", request: RunnerRequest{Mode: RunnerModeImplement}, want: RunnerResultCompleted},
		{name: "review success", request: RunnerRequest{Mode: RunnerModeReview}, want: RunnerResultCompleted},
		{name: "blocked classified", request: RunnerRequest{Mode: RunnerModeImplement}, runErr: blockedSentinel, isBlocked: func(err error) bool { return errors.Is(err, blockedSentinel) }, want: RunnerResultBlocked, contains: "blocked"},
		{name: "timeout maps to blocked", request: RunnerRequest{Mode: RunnerModeImplement, Timeout: time.Minute}, runErr: context.DeadlineExceeded, want: RunnerResultBlocked, contains: "runner timeout"},
		{name: "generic maps to failed", request: RunnerRequest{Mode: RunnerModeImplement}, runErr: errors.New("boom"), want: RunnerResultFailed, contains: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeBackendRunnerResult(started, finished, tt.request, tt.runErr, tt.isBlocked)
			if got.Status != tt.want {
				t.Fatalf("status=%s want=%s", got.Status, tt.want)
			}
			if got.StartedAt != started || got.FinishedAt != finished {
				t.Fatalf("unexpected timestamps: %#v", got)
			}
			if tt.contains != "" && !strings.Contains(got.Reason, tt.contains) {
				t.Fatalf("expected reason to contain %q, got %q", tt.contains, got.Reason)
			}
		})
	}
}
