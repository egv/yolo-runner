package arcreview

import "testing"

func TestPlanNextPRRunnerAction(t *testing.T) {
	tests := []struct {
		name            string
		state           PRRuntimeState
		handledRevision string
		allowShip       bool
		want            PRRunnerAction
	}{
		{
			name: "terminal PR waits",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "merged", Revision: "r2"},
				OpenIssues: []PRIssue{
					{ID: "issue-1", Status: "open", Severity: "blocker", Message: "still open"},
				},
				Comments: []PRComment{
					{ID: "comment-1", Body: "please fix"},
				},
				Checks: []PRCheck{
					{Name: "ci", Status: "pending"},
				},
			},
			handledRevision: "r1",
			allowShip:       true,
			want:            PRRunnerActionWait,
		},
		{
			name: "new revision reviews",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				Checks: []PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r1",
			allowShip:       true,
			want:            PRRunnerActionReview,
		},
		{
			name: "unanswered comments answer",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				Comments: []PRComment{
					{ID: "comment-1", Body: "please explain this"},
					{ID: "comment-2", Body: "resolved already", Resolved: true},
					{ID: "comment-3", Body: "answered already", Answered: true},
				},
				Checks: []PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			want:            PRRunnerActionAnswer,
		},
		{
			name: "open blockers review",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				OpenIssues: []PRIssue{
					{ID: "issue-1", Status: "open", Severity: "blocker", Message: "test is failing"},
				},
				Checks: []PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			want:            PRRunnerActionReview,
		},
		{
			name: "pending checks wait",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				Checks: []PRCheck{
					{Name: "ci", Status: "pending"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			want:            PRRunnerActionWait,
		},
		{
			name: "ship candidate ships",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				Comments: []PRComment{
					{ID: "comment-1", Body: "thanks", Answered: true},
				},
				Checks: []PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			want:            PRRunnerActionShip,
		},
		{
			name: "ship candidate waits when shipping disabled",
			state: PRRuntimeState{
				Revision: "r2",
				Details:  PRDetails{Status: "open", Revision: "r2"},
				Checks: []PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       false,
			want:            PRRunnerActionWait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanNextPRRunnerAction(tt.state, tt.handledRevision, tt.allowShip); got != tt.want {
				t.Fatalf("PlanNextPRRunnerAction() = %q, want %q", got, tt.want)
			}
		})
	}
}
