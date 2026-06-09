package arcreview

import (
	"context"
	"fmt"
	"strings"
)

type ReviewArcanumClient interface {
	PostReviewInlineComment(ctx context.Context, prID string, revision string, comment ReviewInlineComment) error
	PostReviewSummary(ctx context.Context, prID string, revision string, body string) error
}

type ReviewedRevisionStore interface {
	StoreReviewedRevision(ctx context.Context, prID string, revision string) error
}

type ReviewApplier struct {
	Client ReviewArcanumClient
	Store  ReviewedRevisionStore
}

func (a ReviewApplier) Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ReviewResult, error) {
	result, err := ParseReviewResult(payload)
	if err != nil {
		return ReviewResult{}, err
	}
	if a.Client == nil {
		return ReviewResult{}, fmt.Errorf("review Arcanum client is required")
	}
	if a.Store == nil {
		return ReviewResult{}, fmt.Errorf("reviewed revision store is required")
	}

	prID := currentReviewPRID(state)
	if prID == "" {
		return ReviewResult{}, fmt.Errorf("PR ID is required")
	}
	revision := currentPRRevision(state)
	if revision == "" {
		return ReviewResult{}, fmt.Errorf("revision is required")
	}

	for _, comment := range result.InlineComments {
		if err := a.Client.PostReviewInlineComment(ctx, prID, revision, comment); err != nil {
			return ReviewResult{}, fmt.Errorf("post review inline comment: %w", err)
		}
	}
	if err := a.Client.PostReviewSummary(ctx, prID, revision, result.Summary); err != nil {
		return ReviewResult{}, fmt.Errorf("post review summary: %w", err)
	}
	if err := a.Store.StoreReviewedRevision(ctx, prID, revision); err != nil {
		return ReviewResult{}, fmt.Errorf("store reviewed revision: %w", err)
	}
	return result, nil
}

func currentReviewPRID(state PRRuntimeState) string {
	if id := strings.TrimSpace(state.Details.ID); id != "" {
		return id
	}
	return strings.TrimSpace(state.PRID)
}
