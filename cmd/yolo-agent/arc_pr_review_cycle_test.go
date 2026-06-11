package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestArcPRReviewCycleDispatchesSinglePlannedAction(t *testing.T) {
	tests := []struct {
		name            string
		state           arcreview.PRRuntimeState
		handledRevision string
		allowShip       bool
		modelPayload    []byte
		wantAction      arcreview.PRRunnerAction
		wantModelCalls  int
		wantReviewCalls int
		wantReplyCalls  int
		wantShipCalls   int
	}{
		{
			name: "new revision applies review",
			state: arcreview.PRRuntimeState{
				PRID:     "42",
				Revision: "r2",
				Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r2"},
				Checks: []arcreview.PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r1",
			allowShip:       true,
			modelPayload:    []byte("review payload"),
			wantAction:      arcreview.PRRunnerActionReview,
			wantModelCalls:  1,
			wantReviewCalls: 1,
		},
		{
			name: "unanswered comment applies reply",
			state: arcreview.PRRuntimeState{
				PRID:     "42",
				Revision: "r2",
				Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r2"},
				Comments: []arcreview.PRComment{
					{ID: "comment-1", Body: "please explain this"},
				},
				Checks: []arcreview.PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			modelPayload:    []byte("reply payload"),
			wantAction:      arcreview.PRRunnerActionAnswer,
			wantModelCalls:  1,
			wantReplyCalls:  1,
		},
		{
			name: "ship ready with allow ship calls gate",
			state: arcreview.PRRuntimeState{
				PRID:     "42",
				Revision: "r2",
				Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r2"},
				Checks: []arcreview.PRCheck{
					{Name: "ci", Status: "passed"},
				},
			},
			handledRevision: "r2",
			allowShip:       true,
			wantAction:      arcreview.PRRunnerActionShip,
			wantShipCalls:   1,
		},
		{
			name: "terminal status is no-op",
			state: arcreview.PRRuntimeState{
				PRID:     "42",
				Revision: "r2",
				Details:  arcreview.PRDetails{ID: "42", Status: "merged", Revision: "r2"},
				Comments: []arcreview.PRComment{
					{ID: "comment-1", Body: "please explain this"},
				},
				OpenIssues: []arcreview.PRIssue{
					{ID: "issue-1", Status: "open", Message: "still open"},
				},
				Checks: []arcreview.PRCheck{
					{Name: "ci", Status: "pending"},
				},
			},
			handledRevision: "r1",
			allowShip:       true,
			wantAction:      arcreview.PRRunnerActionWait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &fakeArcPRReviewCycleFetcher{state: tt.state}
			store := &fakeArcPRReviewCycleRevisionStore{revision: tt.handledRevision}
			model := &fakeArcPRReviewCycleModelHelper{payload: tt.modelPayload}
			reviewApplier := &fakeArcPRReviewCycleReviewApplier{}
			replyApplier := &fakeArcPRReviewCycleReplyApplier{}
			shipGate := &fakeArcPRReviewCycleShipGate{}

			gotAction, err := runArcPRReviewCycle(context.Background(), arcPRReviewCycleConfig{
				PRID:          "42",
				Workspace:     "/workspace",
				RepoRoot:      "/repo",
				Model:         "gpt-cycle",
				Timeout:       2 * time.Minute,
				MaxRetries:    3,
				Metadata:      map[string]string{"phase": "arc_pr_review_cycle"},
				AllowShip:     tt.allowShip,
				StateFetcher:  fetcher,
				RevisionStore: store,
				ModelHelper:   model,
				ReviewApplier: reviewApplier,
				ReplyApplier:  replyApplier,
				ShipGate:      shipGate,
			})
			if err != nil {
				t.Fatalf("runArcPRReviewCycle() error = %v", err)
			}
			if gotAction != tt.wantAction {
				t.Fatalf("runArcPRReviewCycle() action = %q, want %q", gotAction, tt.wantAction)
			}

			if !reflect.DeepEqual(fetcher.calls, []arcPRReviewCycleFetchCall{{workspace: "/workspace", prID: "42"}}) {
				t.Fatalf("fetch calls = %#v", fetcher.calls)
			}
			if !reflect.DeepEqual(store.calls, []string{"42"}) {
				t.Fatalf("revision store calls = %#v", store.calls)
			}
			if len(model.calls) != tt.wantModelCalls {
				t.Fatalf("model calls = %d, want %d", len(model.calls), tt.wantModelCalls)
			}
			for _, call := range model.calls {
				if !reflect.DeepEqual(call.State, tt.state) {
					t.Fatalf("model state mismatch:\ngot:  %#v\nwant: %#v", call.State, tt.state)
				}
				if call.Model != "gpt-cycle" || call.RepoRoot != "/repo" || call.Timeout != 2*time.Minute || call.MaxRetries != 3 {
					t.Fatalf("model input routing fields mismatch: %#v", call)
				}
				if !reflect.DeepEqual(call.Metadata, map[string]string{"phase": "arc_pr_review_cycle"}) {
					t.Fatalf("model metadata = %#v", call.Metadata)
				}
			}

			if len(reviewApplier.calls) != tt.wantReviewCalls {
				t.Fatalf("review applier calls = %d, want %d", len(reviewApplier.calls), tt.wantReviewCalls)
			}
			for _, call := range reviewApplier.calls {
				assertArcPRReviewCycleApplyCall(t, call, tt.state, tt.modelPayload)
			}
			if len(replyApplier.calls) != tt.wantReplyCalls {
				t.Fatalf("reply applier calls = %d, want %d", len(replyApplier.calls), tt.wantReplyCalls)
			}
			for _, call := range replyApplier.calls {
				assertArcPRReviewCycleApplyCall(t, call, tt.state, tt.modelPayload)
			}

			if len(shipGate.calls) != tt.wantShipCalls {
				t.Fatalf("ship gate calls = %d, want %d", len(shipGate.calls), tt.wantShipCalls)
			}
			for _, call := range shipGate.calls {
				if !reflect.DeepEqual(call.State, tt.state) {
					t.Fatalf("ship gate state mismatch:\ngot:  %#v\nwant: %#v", call.State, tt.state)
				}
				if call.ReviewedRevision != tt.handledRevision || call.AllowShip != tt.allowShip {
					t.Fatalf("ship gate request = %#v", call)
				}
			}
		})
	}
}

func assertArcPRReviewCycleApplyCall(t *testing.T, call arcPRReviewCycleApplyCall, wantState arcreview.PRRuntimeState, wantPayload []byte) {
	t.Helper()
	if !reflect.DeepEqual(call.state, wantState) {
		t.Fatalf("applier state mismatch:\ngot:  %#v\nwant: %#v", call.state, wantState)
	}
	if string(call.payload) != string(wantPayload) {
		t.Fatalf("applier payload = %q, want %q", string(call.payload), string(wantPayload))
	}
}

type fakeArcPRReviewCycleFetcher struct {
	state arcreview.PRRuntimeState
	calls []arcPRReviewCycleFetchCall
}

type arcPRReviewCycleFetchCall struct {
	workspace string
	prID      string
}

func (f *fakeArcPRReviewCycleFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	f.calls = append(f.calls, arcPRReviewCycleFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type fakeArcPRReviewCycleRevisionStore struct {
	revision string
	calls    []string
}

func (s *fakeArcPRReviewCycleRevisionStore) GetReviewedRevision(_ context.Context, prID string) (string, error) {
	s.calls = append(s.calls, prID)
	return s.revision, nil
}

type fakeArcPRReviewCycleModelHelper struct {
	payload []byte
	calls   []arcPRReviewModelInput
}

func (m *fakeArcPRReviewCycleModelHelper) RunArcPRReviewModel(_ context.Context, input arcPRReviewModelInput) ([]byte, error) {
	m.calls = append(m.calls, input)
	return m.payload, nil
}

type fakeArcPRReviewCycleReviewApplier struct {
	calls []arcPRReviewCycleApplyCall
}

func (a *fakeArcPRReviewCycleReviewApplier) Apply(_ context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReviewResult, error) {
	a.calls = append(a.calls, arcPRReviewCycleApplyCall{state: state, payload: append([]byte(nil), payload...)})
	return arcreview.ReviewResult{}, nil
}

type fakeArcPRReviewCycleReplyApplier struct {
	calls []arcPRReviewCycleApplyCall
}

func (a *fakeArcPRReviewCycleReplyApplier) Apply(_ context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReplyResult, error) {
	a.calls = append(a.calls, arcPRReviewCycleApplyCall{state: state, payload: append([]byte(nil), payload...)})
	return arcreview.ReplyResult{}, nil
}

type arcPRReviewCycleApplyCall struct {
	state   arcreview.PRRuntimeState
	payload []byte
}

type fakeArcPRReviewCycleShipGate struct {
	calls []arcreview.ShipGateRequest
}

func (g *fakeArcPRReviewCycleShipGate) GateAndShip(_ context.Context, request arcreview.ShipGateRequest) (arcreview.ShipGateResult, error) {
	g.calls = append(g.calls, request)
	return arcreview.ShipGateResult{}, nil
}
