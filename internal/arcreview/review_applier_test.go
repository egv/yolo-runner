package arcreview

import (
	"context"
	"reflect"
	"testing"
)

func TestReviewApplierPostsReviewResultAndStoresRevision(t *testing.T) {
	ctx := context.Background()
	client := &fakeReviewArcanumClient{}
	store := &fakeReviewedRevisionStore{}
	applier := ReviewApplier{
		Client: client,
		Store:  store,
	}

	result, err := applier.Apply(ctx, PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details:  PRDetails{ID: "42", Revision: "r7"},
	}, []byte(`{
		"summary": "Found one blocking issue.",
		"inline_comments": [
			{
				"path": "internal/arcreview/review_applier.go",
				"line": 27,
				"body": "Persist the revision only after comments are posted.",
				"severity": "blocker"
			}
		],
		"replies": [
			{"comment_id": "comment-1", "body": "Out of scope for this applier."}
		],
		"blockers": [
			{
				"kind": "code",
				"path": "internal/arcreview/review_applier.go",
				"line": 27,
				"message": "revision persistence is missing"
			}
		],
		"ship": {
			"verdict": "do_not_ship",
			"reason": "review side effects are not applied"
		}
	}`))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	wantResult := ReviewResult{
		Summary: "Found one blocking issue.",
		InlineComments: []ReviewInlineComment{
			{
				Path:     "internal/arcreview/review_applier.go",
				Line:     27,
				Body:     "Persist the revision only after comments are posted.",
				Severity: "blocker",
			},
		},
		Replies: []ReviewReply{
			{CommentID: "comment-1", Body: "Out of scope for this applier."},
		},
		Blockers: []ReviewBlocker{
			{
				Kind:    "code",
				Path:    "internal/arcreview/review_applier.go",
				Line:    27,
				Message: "revision persistence is missing",
			},
		},
		Ship: ReviewShipDecision{
			Verdict: "do_not_ship",
			Reason:  "review side effects are not applied",
		},
	}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Apply() result mismatch:\ngot:  %#v\nwant: %#v", result, wantResult)
	}

	if !reflect.DeepEqual(client.inlineComments, []postedInlineComment{
		{
			prID:     "42",
			revision: "r7",
			comment: ReviewInlineComment{
				Path:     "internal/arcreview/review_applier.go",
				Line:     27,
				Body:     "Persist the revision only after comments are posted.",
				Severity: "blocker",
			},
		},
	}) {
		t.Fatalf("inline comments mismatch:\ngot: %#v", client.inlineComments)
	}
	if !reflect.DeepEqual(client.summaries, []postedSummary{
		{prID: "42", revision: "r7", body: "Found one blocking issue."},
	}) {
		t.Fatalf("summaries mismatch:\ngot: %#v", client.summaries)
	}
	if !reflect.DeepEqual(store.reviewedRevisions, []reviewedRevisionRecord{
		{prID: "42", revision: "r7"},
	}) {
		t.Fatalf("reviewed revisions mismatch:\ngot: %#v", store.reviewedRevisions)
	}
}

type fakeReviewArcanumClient struct {
	inlineComments []postedInlineComment
	summaries      []postedSummary
}

type postedInlineComment struct {
	prID     string
	revision string
	comment  ReviewInlineComment
}

type postedSummary struct {
	prID     string
	revision string
	body     string
}

func (c *fakeReviewArcanumClient) PostReviewInlineComment(_ context.Context, prID string, revision string, comment ReviewInlineComment) error {
	c.inlineComments = append(c.inlineComments, postedInlineComment{prID: prID, revision: revision, comment: comment})
	return nil
}

func (c *fakeReviewArcanumClient) PostReviewSummary(_ context.Context, prID string, revision string, body string) error {
	c.summaries = append(c.summaries, postedSummary{prID: prID, revision: revision, body: body})
	return nil
}

type fakeReviewedRevisionStore struct {
	reviewedRevisions []reviewedRevisionRecord
}

type reviewedRevisionRecord struct {
	prID     string
	revision string
}

func (s *fakeReviewedRevisionStore) StoreReviewedRevision(_ context.Context, prID string, revision string) error {
	s.reviewedRevisions = append(s.reviewedRevisions, reviewedRevisionRecord{prID: prID, revision: revision})
	return nil
}
