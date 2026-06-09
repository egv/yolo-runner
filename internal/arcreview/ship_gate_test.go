package arcreview

import (
	"context"
	"strings"
	"testing"
)

func TestShipGateGateAndShipAllowsOnlyFullySafeCase(t *testing.T) {
	tests := []struct {
		name             string
		request          ShipGateRequest
		wantShipped      bool
		wantReasonSubstr string
	}{
		{
			name: "ships fully safe revision",
			request: ShipGateRequest{
				State:             safeShipGateState(),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantShipped: true,
		},
		{
			name: "blocks when shipping disabled",
			request: ShipGateRequest{
				State:             safeShipGateState(),
				ReviewedRevision:  "r7",
				AllowShip:         false,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "shipping is disabled",
		},
		{
			name: "blocks when current revision is not reviewed",
			request: ShipGateRequest{
				State:             safeShipGateState(),
				ReviewedRevision:  "r6",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "current revision r7 is not reviewed",
		},
		{
			name: "blocks with open blockers",
			request: ShipGateRequest{
				State: withShipGateOpenIssues(safeShipGateState(), []PRIssue{
					{ID: "issue-1", Status: "open", Severity: "blocker", Message: "ci is failing"},
				}),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "open blockers",
		},
		{
			name: "blocks with unanswered comments",
			request: ShipGateRequest{
				State: withShipGateComments(safeShipGateState(), []PRComment{
					{ID: "comment-1", Body: "please explain this"},
				}),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "unanswered comments",
		},
		{
			name: "blocks with failed checks",
			request: ShipGateRequest{
				State: withShipGateChecks(safeShipGateState(), []PRCheck{
					{Name: "ci", Status: "failed"},
				}),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "checks failed",
		},
		{
			name: "blocks with pending checks",
			request: ShipGateRequest{
				State: withShipGateChecks(safeShipGateState(), []PRCheck{
					{Name: "ci", Status: "pending"},
				}),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "checks are pending",
		},
		{
			name: "blocks with unknown checks",
			request: ShipGateRequest{
				State: withShipGateChecks(safeShipGateState(), []PRCheck{
					{Name: "ci", Status: "mystery"},
				}),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "ship", Reason: "safe to ship"},
			},
			wantReasonSubstr: "checks have unknown status",
		},
		{
			name: "blocks when model does not approve shipping",
			request: ShipGateRequest{
				State:             safeShipGateState(),
				ReviewedRevision:  "r7",
				AllowShip:         true,
				ModelShipDecision: ReviewShipDecision{Verdict: "do_not_ship", Reason: "needs another pass"},
			},
			wantReasonSubstr: "model did not approve shipping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeShipArcanumClient{}
			result, err := (ShipGate{Client: client}).GateAndShip(context.Background(), tt.request)
			if err != nil {
				t.Fatalf("GateAndShip() error = %v", err)
			}

			if result.Shipped != tt.wantShipped {
				t.Fatalf("GateAndShip() shipped = %v, want %v; reasons=%v", result.Shipped, tt.wantShipped, result.Reasons)
			}
			if gotCalls := len(client.shipCalls); gotCalls != boolToInt(tt.wantShipped) {
				t.Fatalf("Ship() call count = %d, want %d", gotCalls, boolToInt(tt.wantShipped))
			}
			if tt.wantShipped {
				if got := client.shipCalls[0]; got != "42" {
					t.Fatalf("Ship() PR ID = %q, want 42", got)
				}
				if len(result.Reasons) != 0 {
					t.Fatalf("safe case returned reasons: %v", result.Reasons)
				}
				return
			}
			if len(result.Reasons) == 0 {
				t.Fatalf("blocked case returned no rejection reasons")
			}
			if !shipGateReasonsContain(result.Reasons, tt.wantReasonSubstr) {
				t.Fatalf("reasons %v do not contain %q", result.Reasons, tt.wantReasonSubstr)
			}
		})
	}
}

func safeShipGateState() PRRuntimeState {
	return PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details:  PRDetails{ID: "42", Status: "open", Revision: "r7"},
		Comments: []PRComment{
			{ID: "comment-1", Body: "thanks", Answered: true},
			{ID: "comment-2", Body: "resolved", Resolved: true},
		},
		Checks: []PRCheck{
			{Name: "ci", Status: "passed"},
		},
	}
}

func withShipGateOpenIssues(state PRRuntimeState, issues []PRIssue) PRRuntimeState {
	state.OpenIssues = issues
	return state
}

func withShipGateComments(state PRRuntimeState, comments []PRComment) PRRuntimeState {
	state.Comments = comments
	return state
}

func withShipGateChecks(state PRRuntimeState, checks []PRCheck) PRRuntimeState {
	state.Checks = checks
	return state
}

type fakeShipArcanumClient struct {
	shipCalls []string
}

func (c *fakeShipArcanumClient) Ship(_ context.Context, prID string) error {
	c.shipCalls = append(c.shipCalls, prID)
	return nil
}

func shipGateReasonsContain(reasons []string, substr string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, substr) {
			return true
		}
	}
	return false
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
