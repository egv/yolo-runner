package agent

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestMapResultToTaskUpdate(t *testing.T) {
	tests := []struct {
		name       string
		result     workitem.ImplementResult
		wantStatus contracts.TaskStatus
		wantData   map[string]string
	}{
		{
			name: "completed",
			result: workitem.ImplementResult{
				Status:    string(contracts.RunnerResultCompleted),
				Branch:    "task/t-1",
				CommitSHA: "abc123",
				PRURL:     "https://example.test/pr/1",
				Artifacts: map[string]string{
					"review_verdict": "pass",
				},
			},
			wantStatus: contracts.TaskStatusClosed,
			wantData:   nil,
		},
		{
			name: "blocked with completion retry metadata",
			result: workitem.ImplementResult{
				Status: string(contracts.RunnerResultBlocked),
				Reason: "attempt two still not converged",
				Artifacts: map[string]string{
					"completion_retry_count": "1",
					"completion_addendum":    "Attempt 1 failure: lint failed\nAttempt 2 failure: attempt two still not converged",
					"review_retry_count":     "ignored",
					"commit_sha":             "ignored",
				},
			},
			wantStatus: contracts.TaskStatusBlocked,
			wantData: map[string]string{
				"triage_status":          "blocked",
				"triage_reason":          "attempt two still not converged",
				"decision":               "blocked",
				"reason":                 "attempt two still not converged",
				"completion_retry_count": "1",
				"completion_addendum":    "Attempt 1 failure: lint failed\nAttempt 2 failure: attempt two still not converged",
			},
		},
		{
			name: "blocked with landing metadata",
			result: workitem.ImplementResult{
				Status: string(contracts.RunnerResultBlocked),
				Reason: "merge conflict remediation failed",
				Artifacts: map[string]string{
					"landing_status":  "blocked",
					"auto_commit_sha": "abc123",
				},
			},
			wantStatus: contracts.TaskStatusBlocked,
			wantData: map[string]string{
				"triage_status":   "blocked",
				"triage_reason":   "merge conflict remediation failed",
				"decision":        "blocked",
				"reason":          "merge conflict remediation failed",
				"landing_status":  "blocked",
				"auto_commit_sha": "abc123",
			},
		},
		{
			name: "failed with review retry metadata",
			result: workitem.ImplementResult{
				Status:        string(contracts.RunnerResultFailed),
				Reason:        "review rejected: missing regression test",
				ReviewVerdict: "fail",
				Artifacts: map[string]string{
					"review_retry_count":   "1",
					"review_fail_feedback": "missing regression test",
					"review_feedback":      "ignored",
					"completion_addendum":  "ignored",
				},
			},
			wantStatus: contracts.TaskStatusFailed,
			wantData: map[string]string{
				"triage_status":        "failed",
				"triage_reason":        "review rejected: missing regression test",
				"decision":             "failed",
				"reason":               "review rejected: missing regression test",
				"review_retry_count":   "1",
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotData := mapResultToTaskUpdate(tt.result)
			if gotStatus != tt.wantStatus {
				t.Fatalf("status = %q, want %q", gotStatus, tt.wantStatus)
			}
			if !reflect.DeepEqual(gotData, tt.wantData) {
				t.Fatalf("taskData mismatch:\n got: %#v\nwant: %#v", gotData, tt.wantData)
			}
		})
	}
}
