package arcanum

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

type ReviewArcanumClient struct {
	Workspace string
}

func (c ReviewArcanumClient) PostReviewInlineComment(ctx context.Context, prID string, revision string, comment arcreview.ReviewInlineComment) error {
	body := topLevelInlineReviewCommentBody(revision, comment)
	if err := c.postReviewComment(ctx, prID, body); err != nil {
		return fmt.Errorf("post review inline comment: %w", err)
	}
	return nil
}

func (c ReviewArcanumClient) PostReviewSummary(ctx context.Context, prID string, revision string, body string) error {
	if err := c.postReviewComment(ctx, prID, reviewSummaryCommentBody(revision, body)); err != nil {
		return fmt.Errorf("post review summary: %w", err)
	}
	return nil
}

func (c ReviewArcanumClient) postReviewComment(ctx context.Context, prID string, body string) error {
	_, err := RunWorkspaceArc(
		ctx,
		strings.TrimSpace(c.Workspace),
		"comment",
		"--pr", strings.TrimSpace(prID),
		"--message", strings.TrimSpace(body),
	)
	return err
}

// The arc CLI checked for this task does not expose inline-position flags, so
// inline findings are posted as top-level comments with location context.
func topLevelInlineReviewCommentBody(revision string, comment arcreview.ReviewInlineComment) string {
	parts := []string{inlineReviewCommentLocation(comment.Path, comment.Line)}
	if severity := strings.TrimSpace(comment.Severity); severity != "" {
		parts = append(parts, "["+severity+"]")
	}
	if revision := strings.TrimSpace(revision); revision != "" {
		parts = append(parts, "(revision "+revision+")")
	}
	return strings.Join(parts, " ") + "\n\n" + strings.TrimSpace(comment.Body)
}

func inlineReviewCommentLocation(path string, line int) string {
	path = strings.TrimSpace(path)
	switch {
	case path != "" && line > 0:
		return fmt.Sprintf("%s:%d", path, line)
	case path != "":
		return path
	case line > 0:
		return fmt.Sprintf("line %d", line)
	default:
		return "review comment"
	}
}

func reviewSummaryCommentBody(revision string, body string) string {
	revision = strings.TrimSpace(revision)
	body = strings.TrimSpace(body)
	if revision == "" {
		return body
	}
	return fmt.Sprintf("Review summary for revision %s:\n\n%s", revision, body)
}
