package arcreview

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestPRReviewCycleDispatchesSinglePlannedAction(t *testing.T) {
	state := PRRuntimeState{
		PRID:     "42",
		Revision: "r2",
		Details:  PRDetails{ID: "42", Status: "open", Revision: "r2"},
		Checks: []PRCheck{
			{Name: "ci", Status: "passed"},
		},
	}
	fetcher := &fakePRReviewCycleFetcher{state: state}
	store := &fakePRReviewCycleRevisionStore{revision: "r1"}
	model := &fakePRReviewCycleModelHelper{payload: []byte("review payload")}
	reviewApplier := &fakePRReviewCycleReviewApplier{}
	replyApplier := &fakePRReviewCycleReplyApplier{}
	shipGate := &fakePRReviewCycleShipGate{}

	gotAction, err := RunPRReviewCycle(context.Background(), PRReviewCycleConfig{
		PRID:          "42",
		Workspace:     "/workspace",
		RepoRoot:      "/repo",
		Model:         "gpt-cycle",
		Timeout:       2 * time.Minute,
		MaxRetries:    3,
		Metadata:      map[string]string{"phase": "arc_pr_review_cycle"},
		AllowShip:     true,
		StateFetcher:  fetcher,
		RevisionStore: store,
		ModelHelper:   model,
		ReviewApplier: reviewApplier,
		ReplyApplier:  replyApplier,
		ShipGate:      shipGate,
	})
	if err != nil {
		t.Fatalf("RunPRReviewCycle() error = %v", err)
	}
	if gotAction != PRRunnerActionReview {
		t.Fatalf("RunPRReviewCycle() action = %q, want %q", gotAction, PRRunnerActionReview)
	}

	if !reflect.DeepEqual(fetcher.calls, []prReviewCycleFetchCall{{workspace: "/workspace", prID: "42"}}) {
		t.Fatalf("fetch calls = %#v", fetcher.calls)
	}
	if !reflect.DeepEqual(store.calls, []string{"42"}) {
		t.Fatalf("revision store calls = %#v", store.calls)
	}
	if len(model.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.calls))
	}
	modelInput := model.calls[0]
	if !reflect.DeepEqual(modelInput.State, state) {
		t.Fatalf("model state mismatch:\ngot:  %#v\nwant: %#v", modelInput.State, state)
	}
	if modelInput.Model != "gpt-cycle" || modelInput.RepoRoot != "/repo" || modelInput.Timeout != 2*time.Minute || modelInput.MaxRetries != 3 {
		t.Fatalf("model input routing fields mismatch: %#v", modelInput)
	}
	if !reflect.DeepEqual(modelInput.Metadata, map[string]string{"phase": "arc_pr_review_cycle"}) {
		t.Fatalf("model metadata = %#v", modelInput.Metadata)
	}

	if len(reviewApplier.calls) != 1 {
		t.Fatalf("review applier calls = %d, want 1", len(reviewApplier.calls))
	}
	assertPRReviewCycleApplyCall(t, reviewApplier.calls[0], state, []byte("review payload"))
	if len(replyApplier.calls) != 0 {
		t.Fatalf("reply applier calls = %d, want 0", len(replyApplier.calls))
	}
	if len(shipGate.calls) != 0 {
		t.Fatalf("ship gate calls = %d, want 0", len(shipGate.calls))
	}
}

func assertPRReviewCycleApplyCall(t *testing.T, call prReviewCycleApplyCall, wantState PRRuntimeState, wantPayload []byte) {
	t.Helper()
	if !reflect.DeepEqual(call.state, wantState) {
		t.Fatalf("applier state mismatch:\ngot:  %#v\nwant: %#v", call.state, wantState)
	}
	if string(call.payload) != string(wantPayload) {
		t.Fatalf("applier payload = %q, want %q", string(call.payload), string(wantPayload))
	}
}

type fakePRReviewCycleFetcher struct {
	state PRRuntimeState
	calls []prReviewCycleFetchCall
}

type prReviewCycleFetchCall struct {
	workspace string
	prID      string
}

func (f *fakePRReviewCycleFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (PRRuntimeState, error) {
	f.calls = append(f.calls, prReviewCycleFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type fakePRReviewCycleRevisionStore struct {
	revision string
	calls    []string
}

func (s *fakePRReviewCycleRevisionStore) GetReviewedRevision(_ context.Context, prID string) (string, error) {
	s.calls = append(s.calls, prID)
	return s.revision, nil
}

type fakePRReviewCycleModelHelper struct {
	payload []byte
	calls   []PRReviewModelInput
}

func (m *fakePRReviewCycleModelHelper) RunArcPRReviewModel(_ context.Context, input PRReviewModelInput) ([]byte, error) {
	m.calls = append(m.calls, input)
	return m.payload, nil
}

type fakePRReviewCycleReviewApplier struct {
	calls []prReviewCycleApplyCall
}

func (a *fakePRReviewCycleReviewApplier) Apply(_ context.Context, state PRRuntimeState, payload []byte) (ReviewResult, error) {
	a.calls = append(a.calls, prReviewCycleApplyCall{state: state, payload: append([]byte(nil), payload...)})
	return ReviewResult{}, nil
}

type fakePRReviewCycleReplyApplier struct {
	calls []prReviewCycleApplyCall
}

func (a *fakePRReviewCycleReplyApplier) Apply(_ context.Context, state PRRuntimeState, payload []byte) (ReplyResult, error) {
	a.calls = append(a.calls, prReviewCycleApplyCall{state: state, payload: append([]byte(nil), payload...)})
	return ReplyResult{}, nil
}

type prReviewCycleApplyCall struct {
	state   PRRuntimeState
	payload []byte
}

type fakePRReviewCycleShipGate struct {
	calls []ShipGateRequest
}

func (g *fakePRReviewCycleShipGate) GateAndShip(_ context.Context, request ShipGateRequest) (ShipGateResult, error) {
	g.calls = append(g.calls, request)
	return ShipGateResult{}, nil
}
