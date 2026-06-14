package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/sources/arcpr"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceArcPROnceSubmitsAndConsumesOnePRReview(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")
	statePath := filepath.Join(repoRoot, "state", "arcpr.db")

	originalConfigService := newSourceArcPRConfigService
	originalLister := sourceArcPRLister
	originalStateFetcher := sourceArcPRStateFetcher
	originalReplyApplier := sourceArcPRReplyApplier
	t.Cleanup(func() {
		newSourceArcPRConfigService = originalConfigService
		sourceArcPRLister = originalLister
		sourceArcPRStateFetcher = originalStateFetcher
		sourceArcPRReplyApplier = originalReplyApplier
	})
	newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
		return staticArcReviewWatchConfigResolver{
			cfg: arcReviewWatchConfig{
				PollInterval: time.Second,
				LockPath:     filepath.Join(repoRoot, "locks", "arcpr.lock"),
				StatePath:    statePath,
				Reviewer:     "alice",
				AllowShip:    true,
				Workspaces:   []string{"/arcadia/reviews/a"},
				Branches:     []string{"trunk"},
			},
		}
	}

	// Stub the reply applier so result consumption stays offline instead of
	// reaching the live Arcanum API.
	sourceArcPRReplyApplier = arcPRReplyApplierFunc(func(_ context.Context, _ arcreview.PRRuntimeState, _ []byte) (arcreview.ReplyResult, error) {
		return arcreview.ReplyResult{}, nil
	})

	var listedWorkspaces []string
	var fetchedPRs []string
	sourceArcPRLister = arcpr.PRListerFunc(func(_ context.Context, workspace string) ([]arcanum.PRSummary, error) {
		listedWorkspaces = append(listedWorkspaces, workspace)
		return []arcanum.PRSummary{
			{ID: "777", Reviewers: []string{"alice"}, Branch: "trunk", Status: "open"},
			{ID: "wrong-reviewer", Reviewers: []string{"bob"}, Branch: "trunk", Status: "open"},
			{ID: "wrong-branch", Reviewers: []string{"alice"}, Branch: "release", Status: "open"},
		}, nil
	})
	sourceArcPRStateFetcher = arcpr.PRStateFetcherFunc(func(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
		fetchedPRs = append(fetchedPRs, workspace+":"+prID)
		return arcreview.PRRuntimeState{
			PRID:     prID,
			Revision: "rev-777",
			Details: arcreview.PRDetails{
				ID:       prID,
				Status:   "open",
				Revision: "rev-777",
			},
			Comments: []arcreview.PRComment{{ID: "c1", Body: "please update this"}},
		}, nil
	})

	runCalled := false
	code := RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr submit exit code 0, got %d", code)
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for source arcpr")
	}
	if want := []string{"/arcadia/reviews/a"}; !reflect.DeepEqual(listedWorkspaces, want) {
		t.Fatalf("listed workspaces = %#v, want %#v", listedWorkspaces, want)
	}
	if want := []string{"/arcadia/reviews/a:777"}; !reflect.DeepEqual(fetchedPRs, want) {
		t.Fatalf("fetched PRs = %#v, want %#v", fetchedPRs, want)
	}

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected queued PR review item")
	}
	if claimed.Kind != workitem.KindPRReview {
		t.Fatalf("claimed kind = %q, want %q", claimed.Kind, workitem.KindPRReview)
	}
	if claimed.Source != "arcpr-arc-dev" {
		t.Fatalf("claimed source = %q, want arcpr-arc-dev", claimed.Source)
	}
	if claimed.SourceRef != "pr:777" {
		t.Fatalf("claimed source ref = %q, want pr:777", claimed.SourceRef)
	}
	payload, err := workitem.DecodePRReviewPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.PRID != "777" || payload.Revision != "rev-777" || !payload.Ship || !reflect.DeepEqual(payload.UnansweredCommentIDs, []string{"c1"}) {
		t.Fatalf("payload = %#v, want PR 777 rev-777 comment c1 ship=true", payload)
	}

	resultPayload, err := json.Marshal(workitem.PRReviewResult{
		Replies:          []workitem.PRReviewReply{{CommentID: "c1", Body: "handled"}},
		ReviewVerdict:    "ok",
		ShipReady:        true,
		RevisionReviewed: "rev-777",
	})
	if err != nil {
		t.Fatalf("marshal result payload: %v", err)
	}
	if err := store.Complete(claimed.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(queue) error = %v", err)
	}

	code = RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for source arcpr")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr consume exit code 0, got %d", code)
	}

	state, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open(state) error = %v", err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("Close(state) error = %v", err)
		}
	})
	revision, err := state.GetReviewedRevision(ctx, "777")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if revision != "rev-777" {
		t.Fatalf("reviewed revision = %q, want rev-777", revision)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "777")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"c1"}) {
		t.Fatalf("answered comments = %#v, want [c1]", answered)
	}

	store, err = workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue after consume) error = %v", err)
	}
	defer store.Close()
	unconsumed, err := store.ListUnconsumedResults("arcpr-arc-dev")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("unconsumed results = %d, want 0", len(unconsumed))
	}
	next, err := store.Claim("runner-b", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(after consume) error = %v", err)
	}
	if next != nil {
		t.Fatalf("expected no duplicate PR review item after writeback, got %#v", next)
	}
}

type arcPRReplyApplierFunc func(context.Context, arcreview.PRRuntimeState, []byte) (arcreview.ReplyResult, error)

func (f arcPRReplyApplierFunc) Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReplyResult, error) {
	return f(ctx, state, payload)
}

type staticArcReviewWatchConfigResolver struct {
	cfg arcReviewWatchConfig
}

func (r staticArcReviewWatchConfigResolver) ResolveArcReviewWatchConfig(string) (arcReviewWatchConfig, error) {
	return r.cfg, nil
}
