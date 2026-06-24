package arcreview

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestResolveApplierResolvesKnownUnresolvedCommentsAndDedupsOnRetry(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := arcreviewstate.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	client := &fakeResolveArcanumClient{}
	applier := ResolveApplier{
		Client: client,
		Store:  store,
	}
	runtimeState := PRRuntimeState{
		PRID: "42",
		Details: PRDetails{
			ID: "42",
		},
		Comments: []PRComment{
			{ID: "comment-1", Body: "Can this race?"},
			{ID: "comment-2", Body: "Please add coverage."},
		},
	}
	payload := []byte(`{"resolved_comment_ids": ["comment-1", "comment-2"]}`)

	result, err := applier.Apply(ctx, runtimeState, payload)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	wantIDs := []string{"comment-1", "comment-2"}
	if !reflect.DeepEqual(result.ResolvedCommentIDs, wantIDs) {
		t.Fatalf("Apply() resolved IDs mismatch:\ngot:  %#v\nwant: %#v", result.ResolvedCommentIDs, wantIDs)
	}
	if !reflect.DeepEqual(client.resolved, []resolvedComment{
		{prID: "42", commentID: "comment-1"},
		{prID: "42", commentID: "comment-2"},
	}) {
		t.Fatalf("resolved comments mismatch:\ngot: %#v", client.resolved)
	}

	answered, err := store.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, wantIDs) {
		t.Fatalf("answered IDs mismatch:\ngot:  %#v\nwant: %#v", answered, wantIDs)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// The resolved IDs are persisted, so re-applying the same plan must not
	// resolve anything a second time.
	reopened, err := arcreviewstate.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	retryClient := &fakeResolveArcanumClient{}
	retryApplier := ResolveApplier{
		Client: retryClient,
		Store:  reopened,
	}
	if _, err := retryApplier.Apply(ctx, runtimeState, payload); err != nil {
		t.Fatalf("retry Apply() error = %v", err)
	}
	if len(retryClient.resolved) != 0 {
		t.Fatalf("retry resolved comments, want none: %#v", retryClient.resolved)
	}
}

func TestResolveApplierSkipsAlreadyResolvedComments(t *testing.T) {
	ctx := context.Background()
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	client := &fakeResolveArcanumClient{}
	applier := ResolveApplier{
		Client: client,
		Store:  store,
	}
	runtimeState := PRRuntimeState{
		PRID: "42",
		Details: PRDetails{
			ID: "42",
		},
		Comments: []PRComment{
			{ID: "comment-1", Body: "Can this race?", Resolved: true},
			{ID: "comment-2", Body: "Please add coverage."},
		},
	}
	payload := []byte(`{"resolved_comment_ids": ["comment-1", "comment-2"]}`)

	if _, err := applier.Apply(ctx, runtimeState, payload); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !reflect.DeepEqual(client.resolved, []resolvedComment{
		{prID: "42", commentID: "comment-2"},
	}) {
		t.Fatalf("resolved comments mismatch, want only comment-2:\ngot: %#v", client.resolved)
	}
}

func TestResolveApplierRejectsUnknownCommentID(t *testing.T) {
	ctx := context.Background()
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	applier := ResolveApplier{
		Client: &fakeResolveArcanumClient{},
		Store:  store,
	}
	runtimeState := PRRuntimeState{
		PRID: "42",
		Details: PRDetails{
			ID: "42",
		},
		Comments: []PRComment{
			{ID: "comment-1", Body: "Can this race?"},
		},
	}
	payload := []byte(`{"resolved_comment_ids": ["comment-1", "ghost-comment"]}`)

	if _, err := applier.Apply(ctx, runtimeState, payload); err == nil {
		t.Fatalf("Apply() error = nil, want error for unknown comment id")
	}
}

func TestResolveApplierDedupsWithinPayload(t *testing.T) {
	ctx := context.Background()
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	client := &fakeResolveArcanumClient{}
	applier := ResolveApplier{
		Client: client,
		Store:  store,
	}
	runtimeState := PRRuntimeState{
		PRID: "42",
		Details: PRDetails{
			ID: "42",
		},
		Comments: []PRComment{
			{ID: "comment-1", Body: "Can this race?"},
		},
	}
	// A single run must resolve each comment at most once even if the plan
	// lists it twice.
	payload := []byte(`{"resolved_comment_ids": ["comment-1", "comment-1"]}`)

	if _, err := applier.Apply(ctx, runtimeState, payload); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !reflect.DeepEqual(client.resolved, []resolvedComment{
		{prID: "42", commentID: "comment-1"},
	}) {
		t.Fatalf("resolved comments mismatch, want single resolve:\ngot: %#v", client.resolved)
	}
}

type fakeResolveArcanumClient struct {
	resolved []resolvedComment
}

type resolvedComment struct {
	prID      string
	commentID string
}

func (c *fakeResolveArcanumClient) ResolveComment(_ context.Context, prID string, commentID string) error {
	c.resolved = append(c.resolved, resolvedComment{prID: prID, commentID: commentID})
	return nil
}
